#!/usr/bin/env python3
"""
Оркестратор задач Claude Manager.

Автоматический запуск сессий Claude Code для последовательного
выполнения задач из TASKS.md и HARNESS-TASKS.md с учётом зависимостей.

Использование:
    python scripts/orchestrator.py                          # запуск с первой доступной задачи
    python scripts/orchestrator.py --max-tasks 3            # максимум 3 задачи, потом стоп
    python scripts/orchestrator.py --task TASK-05           # начать с конкретной задачи
    python scripts/orchestrator.py --task HARNESS-01        # начать с харнес-задачи
    python scripts/orchestrator.py --model haiku            # стартовать на Haiku
    python scripts/orchestrator.py --fallback-model haiku   # резерв при rate limit
    python scripts/orchestrator.py --new                    # стартовать с нуля (без --resume)
    python scripts/orchestrator.py --stop                   # мягкая остановка
    python scripts/orchestrator.py --status                 # статус всех задач
    python scripts/orchestrator.py --reset TASK-05          # сбросить задачу
    python scripts/orchestrator.py --reset HARNESS-01       # сбросить харнес-задачу
    python scripts/orchestrator.py --reset-all              # сбросить всё

Выбор модели
------------
По умолчанию `claude` запускается без `--model` — CLI берёт настроенную модель.
Алиасы: haiku → claude-haiku-4-5, sonnet → claude-sonnet-4-6, opus → claude-opus-4-7.
Также через env CM_MODEL / CM_FALLBACK_MODEL.

Fallback при rate limit
-----------------------
При первом rate limit оркестратор переключается на резервную модель без 5-минутной паузы.
Если резервная тоже исчерпана — стандартная пауза 5 мин.
Интерактивное меню, если --fallback-model не задан (и не задан CM_FALLBACK_MODEL).

Crash recovery
--------------
При старте каждой задачи оркестратор пишет scripts/.session.json с {task_id, started_at}.
session_id захватывается из первого stream-json события и дописывается сразу.
При нормальном завершении файл удаляется.
При следующем запуске — возобновление через `claude --resume <session_id>`.
Флаг --new удаляет .session.json и стартует без --resume.
"""

import argparse
import atexit
import ctypes
import json
import os
import re
import signal
import subprocess
import sys
import threading
import time
import traceback
from datetime import datetime, timedelta
from pathlib import Path

# Корень проекта — два уровня вверх от scripts/
PROJECT_DIR = Path(__file__).resolve().parent.parent
SCRIPTS_DIR = Path(__file__).resolve().parent
TASKS_FILE = PROJECT_DIR / "TASKS.md"
HARNESS_FILE = PROJECT_DIR / "HARNESS-TASKS.md"
STATE_FILE = SCRIPTS_DIR / ".taskstate.json"
SESSION_FILE = SCRIPTS_DIR / ".session.json"
STOP_FILE = SCRIPTS_DIR / ".stop"
JOBSTATUS_FILE = SCRIPTS_DIR / ".jobstatus"

# Принудительно UTF-8 для stdout/stderr на Windows.
for _stream in (sys.stdout, sys.stderr):
    if hasattr(_stream, "reconfigure"):
        _stream.reconfigure(encoding="utf-8", errors="replace")


# ─── Process cleanup ─────────────────────────────────────────────

_active_process: subprocess.Popen | None = None
_process_lock = threading.Lock()


def _cleanup() -> None:
    with _process_lock:
        proc = _active_process
    if proc is not None and proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()


def _sighandler(sig, frame) -> None:
    _cleanup()
    sys.exit(0)


atexit.register(_cleanup)
signal.signal(signal.SIGINT, _sighandler)
signal.signal(signal.SIGTERM, _sighandler)

if os.name == "nt":
    @ctypes.WINFUNCTYPE(ctypes.c_bool, ctypes.c_uint)
    def _win_ctrl_handler(ctrl_type: int) -> bool:
        _cleanup()
        return False

    ctypes.windll.kernel32.SetConsoleCtrlHandler(_win_ctrl_handler, True)


# ─── Windows process tree cleanup ────────────────────────────────

if os.name == "nt":
    _k32 = ctypes.windll.kernel32
    _k32.CreateToolhelp32Snapshot.restype = ctypes.c_void_p
    _k32.CreateToolhelp32Snapshot.argtypes = [ctypes.c_uint32, ctypes.c_uint32]
    _k32.Process32First.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
    _k32.Process32Next.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
    _k32.CloseHandle.argtypes = [ctypes.c_void_p]
    _k32.OpenProcess.restype = ctypes.c_void_p
    _k32.TerminateProcess.argtypes = [ctypes.c_void_p, ctypes.c_uint]
    _INVALID_HANDLE = ctypes.c_void_p(-1).value

    class _PROCESSENTRY32(ctypes.Structure):
        _fields_ = [
            ("dwSize",              ctypes.c_ulong),
            ("cntUsage",            ctypes.c_ulong),
            ("th32ProcessID",       ctypes.c_ulong),
            ("th32DefaultHeapID",   ctypes.c_size_t),
            ("th32ModuleID",        ctypes.c_ulong),
            ("cntThreads",          ctypes.c_ulong),
            ("th32ParentProcessID", ctypes.c_ulong),
            ("pcPriClassBase",      ctypes.c_long),
            ("dwFlags",             ctypes.c_ulong),
            ("szExeFile",           ctypes.c_char * 260),
        ]

    def _snapshot_descendants(root_pid: int) -> set:
        TH32CS_SNAPPROCESS = 0x00000002
        snap = _k32.CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0)
        if snap is None or snap == _INVALID_HANDLE:
            return set()
        parent_to_children: dict = {}
        entry = _PROCESSENTRY32()
        entry.dwSize = ctypes.sizeof(_PROCESSENTRY32)
        try:
            ok = _k32.Process32First(snap, ctypes.byref(entry))
            while ok:
                parent_to_children.setdefault(
                    entry.th32ParentProcessID, []
                ).append(entry.th32ProcessID)
                ok = _k32.Process32Next(snap, ctypes.byref(entry))
        finally:
            _k32.CloseHandle(snap)
        result: set = set()
        queue = [root_pid]
        while queue:
            pid = queue.pop()
            for child in parent_to_children.get(pid, []):
                result.add(child)
                queue.append(child)
        return result

    def _kill_pids(pids: set) -> int:
        PROCESS_TERMINATE = 0x0001
        killed = 0
        for pid in pids:
            handle = _k32.OpenProcess(PROCESS_TERMINATE, False, pid)
            if handle:
                if _k32.TerminateProcess(handle, 1):
                    killed += 1
                _k32.CloseHandle(handle)
        return killed

else:
    def _snapshot_descendants(root_pid: int) -> set:  # type: ignore[misc]
        return set()

    def _kill_pids(pids: set) -> int:  # type: ignore[misc]
        return 0


# ─── Model aliases ────────────────────────────────────────────────

MODEL_ALIASES: dict[str, str] = {
    "haiku":  "claude-haiku-4-5",
    "sonnet": "claude-sonnet-4-6",
    "opus":   "claude-opus-4-7",
}

PREDEFINED_FALLBACKS: list[tuple[str, str]] = [
    ("haiku",  "самая быстрая, отдельные щедрые лимиты (рекомендуется)"),
    ("sonnet", "баланс скорости и качества"),
    ("opus",   "мощная, обычно общие лимиты с Sonnet"),
]

INITIAL_MODEL_ENV = "CM_MODEL"
FALLBACK_MODEL_ENV = "CM_FALLBACK_MODEL"


def resolve_model_alias(name: str | None) -> str | None:
    if not name:
        return None
    key = name.strip().lower()
    if not key:
        return None
    return MODEL_ALIASES.get(key, name.strip())


def choose_fallback_model() -> str:
    border = "=" * 60
    default_alias = PREDEFINED_FALLBACKS[0][0]
    default_model = MODEL_ALIASES[default_alias]
    print(border, flush=True)
    print("Выберите резервную модель для переключения:", flush=True)
    for i, (alias, desc) in enumerate(PREDEFINED_FALLBACKS, 1):
        print(f"  {i}) {alias:<8} — {desc}", flush=True)
    print(f"  {len(PREDEFINED_FALLBACKS) + 1}) Ввести имя модели вручную", flush=True)
    print(border, flush=True)
    print(f"  (для unattended-режима задайте --fallback-model или {FALLBACK_MODEL_ENV})", flush=True)

    while True:
        try:
            raw = input(f"Выбор [1 = {default_alias}]: ").strip()
        except (EOFError, KeyboardInterrupt):
            print(f"\nВвод прерван — использую {default_model}", flush=True)
            return default_model
        if not raw:
            return default_model
        if raw.isdigit():
            idx = int(raw)
            if 1 <= idx <= len(PREDEFINED_FALLBACKS):
                return MODEL_ALIASES[PREDEFINED_FALLBACKS[idx - 1][0]]
            if idx == len(PREDEFINED_FALLBACKS) + 1:
                try:
                    custom = input("Имя модели (alias или claude-*): ").strip()
                except (EOFError, KeyboardInterrupt):
                    return default_model
                resolved = resolve_model_alias(custom)
                if resolved:
                    return resolved
                print("Пустое имя — повторите выбор.", flush=True)
                continue
            print("Неверный номер — повторите выбор.", flush=True)
            continue
        resolved = resolve_model_alias(raw)
        if resolved:
            return resolved


def resolve_fallback_model(preset: str | None) -> str:
    if preset:
        return preset
    env_resolved = resolve_model_alias(os.environ.get(FALLBACK_MODEL_ENV))
    if env_resolved:
        return env_resolved
    return choose_fallback_model()


# ─── Crash recovery ──────────────────────────────────────────────

def save_session_state(task_id: str) -> None:
    state = {
        "task_id": task_id,
        "started_at": datetime.now().isoformat(),
    }
    tmp = SESSION_FILE.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(state, indent=2, ensure_ascii=False), encoding="utf-8")
    os.replace(tmp, SESSION_FILE)


def update_session_id(session_id: str) -> None:
    if not SESSION_FILE.exists():
        return
    try:
        state = json.loads(SESSION_FILE.read_text(encoding="utf-8"))
        if "session_id" not in state:
            state["session_id"] = session_id
            tmp = SESSION_FILE.with_suffix(".json.tmp")
            tmp.write_text(json.dumps(state, indent=2, ensure_ascii=False), encoding="utf-8")
            os.replace(tmp, SESSION_FILE)
    except (json.JSONDecodeError, OSError):
        pass


def load_session_state() -> dict | None:
    if not SESSION_FILE.exists():
        return None
    try:
        return json.loads(SESSION_FILE.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return None


def clear_session_state() -> None:
    SESSION_FILE.unlink(missing_ok=True)


# ─── Logging ─────────────────────────────────────────────────────

def log(message: str):
    ts = datetime.now().strftime("%H:%M:%S")
    print(f"[{ts}] {message}", flush=True)


# ─── Task parsing ────────────────────────────────────────────────

class Task:
    def __init__(self, task_id: str, title: str, depends: list[str], prompt: str):
        self.id = task_id
        self.title = title
        self.depends = depends
        self.prompt = prompt

    def __repr__(self):
        return f"Task({self.id}: {self.title})"


_TASK_ID_RE = re.compile(r"^## ((?:TASK|HARNESS)-\d+):\s*(.+)$", re.MULTILINE)
_DEP_RE = re.compile(r"(?:TASK|HARNESS)-\d+")


def _parse_file(path: Path) -> list[Task]:
    """Парсить один файл задач (TASKS.md или HARNESS-TASKS.md)."""
    content = path.read_text(encoding="utf-8")
    tasks = []
    matches = list(_TASK_ID_RE.finditer(content))

    for i, match in enumerate(matches):
        task_id = match.group(1)
        title = match.group(2).strip()
        start = match.end()
        end = matches[i + 1].start() if i + 1 < len(matches) else len(content)
        section = content[start:end]

        dep_match = re.search(r"\*\*Depends on:\*\*\s*(.+)", section)
        depends = []
        if dep_match:
            dep_text = dep_match.group(1)
            # Берём первую строку — описание зависимостей до переноса
            first_line = dep_text.split("\n")[0].strip()
            if first_line.lower() not in ("nothing", "ничего"):
                depends = _DEP_RE.findall(first_line)

        prompt_match = re.search(
            r"\*\*Prompt:\*\*\s*```\s*\n(.*?)```", section, re.DOTALL
        )
        prompt = prompt_match.group(1).strip() if prompt_match else ""
        tasks.append(Task(task_id, title, depends, prompt))

    return tasks


def parse_tasks() -> list[Task]:
    """Загрузить все задачи: сначала TASKS.md, потом HARNESS-TASKS.md."""
    tasks = _parse_file(TASKS_FILE)
    if HARNESS_FILE.exists():
        tasks += _parse_file(HARNESS_FILE)
    return tasks


# ─── State management ────────────────────────────────────────────

def load_state() -> dict:
    if STATE_FILE.exists():
        return json.loads(STATE_FILE.read_text(encoding="utf-8"))
    return {"completed": {}, "in_progress": None, "failed": {}}


def save_state(state: dict):
    tmp = STATE_FILE.with_suffix(".json.tmp")
    tmp.write_text(
        json.dumps(state, indent=2, ensure_ascii=False), encoding="utf-8"
    )
    os.replace(tmp, STATE_FILE)


def get_next_task(
    tasks: list[Task], state: dict, specific_task: str = None
) -> Task | None:
    completed = set(state.get("completed", {}).keys())
    in_progress = state.get("in_progress")
    if in_progress:
        for task in tasks:
            if task.id == in_progress:
                return task
    if specific_task:
        for task in tasks:
            if task.id == specific_task:
                if task.id in completed:
                    log(f"{task.id} уже выполнена.")
                    return None
                missing = [d for d in task.depends if d not in completed]
                if missing:
                    log(f"{task.id} заблокирована: не выполнены {', '.join(missing)}")
                    return None
                return task
        log(f"Задача {specific_task} не найдена.")
        return None
    for task in tasks:
        if task.id in completed:
            continue
        if all(dep in completed for dep in task.depends):
            return task
    return None


# ─── Status display ──────────────────────────────────────────────

def _show_task_group(
    label: str,
    group: list[Task],
    completed: dict,
    in_progress: str | None,
    failed: dict,
):
    """Вывести одну группу задач (App Tasks или Harness Tasks)."""
    print(f"\n{'─' * 60}")
    print(f"  {label}")
    print(f"{'─' * 60}")

    done_tasks = [t for t in group if t.id in completed]
    if done_tasks:
        print("\n  Выполнено:")
        for task in done_tasks:
            info = completed[task.id]
            dur = info.get("duration", "?")
            cost = info.get("cost_usd", 0)
            ts = info.get("completed_at", "?")
            print(f"    {task.id}: {task.title}")
            print(f"           {dur}, ${cost:.4f}, {ts}")

    in_prog_tasks = [t for t in group if t.id == in_progress]
    if in_prog_tasks:
        print(f"\n  В работе:")
        task = in_prog_tasks[0]
        print(f"    {task.id}: {task.title}")
        sess = load_session_state()
        if sess and sess.get("session_id"):
            print(f"           session_id: {sess['session_id'][:16]}...")

    failed_tasks = [t for t in group if t.id in failed]
    if failed_tasks:
        print(f"\n  С ошибкой:")
        for task in failed_tasks:
            info = failed[task.id]
            print(f"    {task.id}: {task.title} (попыток: {info.get('attempts', 0)})")

    all_completed = set(completed.keys())
    ready, pending = [], []
    for task in group:
        if task.id in completed or task.id == in_progress:
            continue
        if all(dep in all_completed for dep in task.depends):
            ready.append(task)
        else:
            pending.append(task)

    if ready:
        print(f"\n  Готово к запуску:")
        for task in ready:
            print(f"    {task.id}: {task.title}")

    if pending:
        print(f"\n  Заблокировано (ждут зависимости):")
        for task in pending:
            missing = [d for d in task.depends if d not in all_completed]
            print(f"    {task.id}: {task.title}")
            print(f"           <- {', '.join(missing)}")

    total_g = len(group)
    done_g = len(done_tasks)
    pct_g = done_g * 100 // total_g if total_g > 0 else 0
    cost_g = sum(completed[t.id].get("cost_usd", 0) for t in done_tasks)
    print(f"\n  {done_g}/{total_g} ({pct_g}%)  |  ${cost_g:.4f}")


def show_status(tasks: list[Task], state: dict):
    completed = state.get("completed", {})
    in_progress = state.get("in_progress")
    failed = state.get("failed", {})

    app_tasks = [t for t in tasks if t.id.startswith("TASK-")]
    harness_tasks = [t for t in tasks if t.id.startswith("HARNESS-")]

    print("Статус задач Claude Manager:")
    print("=" * 60)

    _show_task_group("App Tasks (TASK-XX)", app_tasks, completed, in_progress, failed)
    if harness_tasks:
        _show_task_group("Harness Tasks (HARNESS-XX)", harness_tasks, completed, in_progress, failed)

    print()
    print("=" * 60)
    total = len(tasks)
    done = len([t for t in tasks if t.id in completed])
    pct = done * 100 // total if total > 0 else 0
    total_cost = sum(info.get("cost_usd", 0) for info in completed.values())
    print(f"Итого: {done}/{total} ({pct}%)  |  Потрачено: ${total_cost:.4f}")

    if JOBSTATUS_FILE.exists():
        content = JOBSTATUS_FILE.read_text(encoding="utf-8")
        for line in content.splitlines():
            if line.startswith("status:"):
                status = line.split(":", 1)[1].strip()
                print(f"Оркестратор: {status}")
                break


# ─── Job status file ─────────────────────────────────────────────

def set_jobstatus(status: str, detail: str = ""):
    ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    lines = [f"status: {status}", f"updated: {ts}"]
    if detail:
        lines.append(f"detail: {detail}")
    JOBSTATUS_FILE.write_text("\n".join(lines), encoding="utf-8")


# ─── Stream-JSON formatting ──────────────────────────────────────

def format_tool_use(block: dict) -> str:
    tool = block.get("name", "?")
    inp = block.get("input", {})
    if tool == "Bash":
        cmd = inp.get("command", "")
        preview = cmd[:120].replace("\n", " ")
        return f"  $ {preview}"
    elif tool == "Read":
        return f"  Читает: {inp.get('file_path', '?')}"
    elif tool == "Edit":
        return f"  Редактирует: {inp.get('file_path', '?')}"
    elif tool == "Write":
        return f"  Пишет: {inp.get('file_path', '?')}"
    elif tool == "Grep":
        return f"  Ищет: {inp.get('pattern', '?')}"
    elif tool == "Glob":
        return f"  Glob: {inp.get('pattern', '?')}"
    elif tool == "Agent":
        return f"  Agent: {inp.get('description', '?')}"
    else:
        return f"  Инструмент: {tool}"


def format_event(event: dict) -> list[str]:
    lines = []
    ev_type = event.get("type", "")

    if ev_type == "result":
        cost = event.get("total_cost_usd", 0)
        if cost:
            lines.append(f"  Стоимость сессии: ${cost:.4f}")
        result_text = event.get("result", "")
        if result_text:
            preview = result_text[:200].replace("\n", " ")
            if len(result_text) > 200:
                preview += "..."
            lines.append(f"  Результат: {preview}")
        return lines

    if ev_type == "assistant":
        msg = event.get("message", {})
        for block in msg.get("content", []):
            btype = block.get("type", "")
            if btype == "tool_use":
                lines.append(format_tool_use(block))
            elif btype == "text":
                text = block.get("text", "")
                if text:
                    preview = text[:200].replace("\n", " ")
                    if len(text) > 200:
                        preview += "..."
                    lines.append(f"  {preview}")
        return lines

    return lines


# ─── Claude runner ────────────────────────────────────────────────

RATE_LIMIT_RE = re.compile(r"resets?\s+(\d{1,2}:\d{2}(?:am|pm)?)", re.IGNORECASE)


def announce_fallback(reason: str, model: str) -> None:
    border = "=" * 60
    log("")
    log(border)
    log("  RATE LIMIT основной модели")
    log(f"  Причина: {reason}")
    log(f"  Переключаюсь на резервную модель: {model}")
    log(f"  Следующий вызов claude пойдёт через {model} БЕЗ паузы.")
    log("  Сбросить fallback можно только перезапуском оркестратора.")
    log(border)
    log("")


def run_claude(
    task: Task,
    model: str | None = None,
    resume_session_id: str | None = None,
) -> tuple[int, bool, bool, float]:
    """Запустить claude. Возвращает (exit_code, rate_limited, auth_error, cost_usd)."""
    global _active_process

    full_prompt = (
        f"Read CLAUDE.md and PLAN.md for project context. "
        f"Your task is {task.id}: {task.title}.\n\n"
        f"{task.prompt}\n\n"
        f"Focus only on this task. Do not work on other tasks. "
        f"Make sure the code compiles when you're done."
    )

    cmd = ["claude", "--dangerously-skip-permissions", "--verbose",
           "--output-format", "stream-json"]
    if model:
        cmd += ["--model", model]
    if resume_session_id:
        cmd += ["--resume", resume_session_id, "-p", full_prompt]
    else:
        cmd += ["-p", full_prompt]

    if model:
        log(f"  Модель: {model}")
    if resume_session_id:
        log(f"  Возобновление сессии: {resume_session_id[:16]}...")

    process = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=PROJECT_DIR,
        text=True,
        encoding="utf-8",
        errors="replace",
    )

    with _process_lock:
        _active_process = process

    # Трекинг дочерних процессов для зачистки после выхода
    _seen: set = set()
    _stop_tracker = threading.Event()

    def _tracker() -> None:
        while not _stop_tracker.wait(timeout=2.0):
            _seen.update(_snapshot_descendants(process.pid))

    tracker = threading.Thread(target=_tracker, daemon=True)
    tracker.start()

    try:
        rate_limited = False
        auth_error = False
        cost_usd = 0.0
        _session_id_captured = resume_session_id is not None

        for line in process.stdout:
            line = line.strip()
            if not line:
                continue

            if "403" in line and ("forbidden" in line.lower() or "authenticate" in line.lower()):
                auth_error = True
                log(f"  Auth error (403): {line[:120]}")
                continue

            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue

            # Захватить session_id из первого события, где он есть
            if not _session_id_captured:
                sid = event.get("session_id", "")
                if sid:
                    update_session_id(sid)
                    _session_id_captured = True

            ev_type = event.get("type", "")
            if ev_type == "rate_limit_event":
                info = event.get("rate_limit_info", {})
                if info.get("status", "").startswith("blocked"):
                    rate_limited = True
                    log("  Rate limit (blocked)")

            if ev_type == "result":
                cost_usd = event.get("total_cost_usd", 0.0) or 0.0

            for display_line in format_event(event):
                log(display_line)

        stderr_output = process.stderr.read()
        if stderr_output:
            sl = stderr_output.lower()
            if "hit your limit" in sl or "rate limit" in sl:
                rate_limited = True
                match = RATE_LIMIT_RE.search(stderr_output)
                if match:
                    log(f"  Rate limit до {match.group(1)}")
                else:
                    log("  Rate limit обнаружен")
            elif "403" in stderr_output and ("forbidden" in sl or "authenticate" in sl):
                auth_error = True
                log("  Auth error (403) в stderr")

        process.wait()
        return process.returncode, rate_limited, auth_error, cost_usd
    finally:
        _stop_tracker.set()
        tracker.join(timeout=3.0)
        with _process_lock:
            if _active_process is process:
                _active_process = None
        killed = _kill_pids(_seen)
        if killed > 0:
            log(f"  Завершено {killed} дочерних процессов после сессии")


def wait_for_rate_limit():
    wait_minutes = 5
    resume_at = (datetime.now() + timedelta(minutes=wait_minutes)).strftime("%H:%M")
    log(f"Rate limit — пауза {wait_minutes} мин (до {resume_at})...")
    set_jobstatus("rate limit", f"до {resume_at}")
    time.sleep(wait_minutes * 60)
    log("Пауза завершена, продолжаю.")


# ─── Duration formatting ─────────────────────────────────────────

def format_duration(seconds: float) -> str:
    m, s = divmod(int(seconds), 60)
    h, m = divmod(m, 60)
    if h > 0:
        return f"{h}ч {m}м {s}с"
    elif m > 0:
        return f"{m}м {s}с"
    else:
        return f"{s}с"


# ─── Main loop ────────────────────────────────────────────────────

def run_task_loop(
    tasks: list[Task],
    max_tasks: int = 0,
    specific_task: str = None,
    fallback_preset: str | None = None,
    force_new: bool = False,
    initial_model: str | None = None,
):
    state = load_state()
    task_count = 0
    total_cost = 0.0
    fallback_model: str | None = None

    log(f"Старт оркестратора. Проект: {PROJECT_DIR}")
    log(f"Задач: {len(tasks)}, выполнено: {len(state.get('completed', {}))}")
    if initial_model:
        log(f"Стартовая модель: {initial_model}")
    log(f"Стоп-файл: {STOP_FILE}")
    log("")
    set_jobstatus("запущен")

    # Принудительный старт с нуля
    if force_new:
        if SESSION_FILE.exists():
            log("Флаг --new: удаляю сохранённое состояние сессии, не возобновляю.")
            clear_session_state()
        else:
            log("Флаг --new: сохранённого состояния нет, стартую с нуля.")

    # Crash recovery
    existing_session = load_session_state()
    if existing_session and not force_new:
        sess_task_id = existing_session.get("task_id")
        session_id = existing_session.get("session_id")
        started = existing_session.get("started_at", "?")
        if session_id and sess_task_id:
            log(f"Найдена прерванная сессия задачи {sess_task_id} (начата {started})")
            log(f"  session_id: {session_id[:16]}...")
            log("Возобновляю через --resume...")

            # Найти задачу по id
            resume_task = next((t for t in tasks if t.id == sess_task_id), None)
            if resume_task:
                task_count += 1
                set_jobstatus("возобновление", f"{sess_task_id}: {resume_task.title}")
                resume_prompt_text = (
                    f"Session was interrupted (crash/terminal close). "
                    f"Run: git status, then read CLAUDE.md. "
                    f"Based on the conversation history above and current git state, "
                    f"determine what was already done and continue the task from where it stopped. "
                    f"Task: {resume_task.id}: {resume_task.title}."
                )
                # Подменяем prompt для resume
                resume_task_copy = Task(
                    resume_task.id, resume_task.title, resume_task.depends, resume_prompt_text
                )
                start_time = time.time()
                try:
                    exit_code, rate_limited, auth_error, cost = run_claude(
                        resume_task_copy,
                        model=fallback_model or initial_model,
                        resume_session_id=session_id,
                    )
                except FileNotFoundError:
                    log("claude не найден в PATH.")
                    clear_session_state()
                    return
                except Exception as e:
                    log(f"Ошибка запуска: {e!r}")
                    clear_session_state()
                    return

                elapsed = time.time() - start_time
                duration = format_duration(elapsed)

                if exit_code == 0:
                    clear_session_state()
                    state = load_state()
                    state["completed"][resume_task.id] = {
                        "completed_at": datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
                        "duration": duration,
                        "cost_usd": cost,
                    }
                    state["in_progress"] = None
                    save_state(state)
                    total_cost += cost
                    log(f"{resume_task.id} (возобновлённая) завершена. ({duration}, ${cost:.4f})")
                elif rate_limited and exit_code != 0:
                    task_count -= 1
                    if fallback_model is None:
                        fallback_model = resolve_fallback_model(fallback_preset)
                        announce_fallback("rate limit при возобновлении", fallback_model)
                        set_jobstatus("fallback model", fallback_model)
                    else:
                        log(f"Резервная модель {fallback_model} тоже исчерпана.")
                        wait_for_rate_limit()
                elif auth_error and exit_code != 0:
                    log("Auth error при возобновлении. Пауза 60 сек...")
                    clear_session_state()
                    task_count -= 1
                    time.sleep(60)
                else:
                    log(f"Возобновление не удалось (код {exit_code}). Продолжаю без --resume.")
                    clear_session_state()
                    task_count -= 1
            else:
                log(f"Задача {sess_task_id} из session.json не найдена в TASKS.md. Сбрасываю.")
                clear_session_state()
        elif sess_task_id:
            log(f"Найдено состояние задачи {sess_task_id} без session_id (сессия не стартовала). Сбрасываю.")
            clear_session_state()

    all_done = False

    while True:
        try:
            if STOP_FILE.exists():
                log("Найден стоп-файл. Останавливаюсь.")
                STOP_FILE.unlink()
                break

            if max_tasks > 0 and task_count >= max_tasks:
                log(f"Достигнут лимит задач ({max_tasks}). Останавливаюсь.")
                break

            state = load_state()
            task = get_next_task(tasks, state, specific_task if task_count == 0 else None)

            if task is None:
                if len(state.get("completed", {})) == len(tasks):
                    log("Все задачи выполнены!")
                    all_done = True
                else:
                    log("Нет доступных задач (зависимости не выполнены или все сделано).")
                break

            task_count += 1
            log(f"{'=' * 60}")
            log(f"Задача #{task_count}: {task.id} — {task.title}")
            if task.depends:
                log(f"  Зависимости: {', '.join(task.depends)}")
            log(f"{'=' * 60}")

            state["in_progress"] = task.id
            save_state(state)
            set_jobstatus("работает", f"{task.id}: {task.title}")

            # Записать состояние ДО запуска — чтобы не потерять при краше
            save_session_state(task.id)

            start_time = time.time()
            log("Запуск claude...")
            try:
                exit_code, rate_limited, auth_error, cost = run_claude(
                    task,
                    model=fallback_model or initial_model,
                )
            except FileNotFoundError:
                log("claude не найден в PATH.")
                clear_session_state()
                break
            except Exception as e:
                log(f"Ошибка запуска: {e!r}")
                clear_session_state()
                break

            elapsed = time.time() - start_time
            duration = format_duration(elapsed)

            if rate_limited and exit_code != 0:
                task_count -= 1
                state["in_progress"] = None
                save_state(state)
                # Не удалять session.json — пригодится для --resume после паузы
                if fallback_model is None:
                    fallback_model = resolve_fallback_model(fallback_preset)
                    announce_fallback(f"задача {task.id}", fallback_model)
                    set_jobstatus("fallback model", fallback_model)
                else:
                    log(f"Резервная модель {fallback_model} тоже исчерпана.")
                    wait_for_rate_limit()

            elif auth_error and exit_code != 0:
                task_count -= 1
                clear_session_state()
                state["in_progress"] = None
                save_state(state)
                log("Auth error (403). Пауза 60 сек перед повтором...")
                time.sleep(60)

            elif exit_code != 0:
                task_count -= 1
                log(f"Claude завершился с кодом {exit_code}. ({duration})")
                clear_session_state()
                failed = state.get("failed", {})
                fail_info = failed.get(task.id, {"attempts": 0})
                fail_info["attempts"] += 1
                fail_info["last_attempt"] = datetime.now().strftime("%H:%M:%S")
                failed[task.id] = fail_info
                state["failed"] = failed
                state["in_progress"] = None
                save_state(state)
                if fail_info["attempts"] >= 3:
                    log(f"{task.id} провалилась {fail_info['attempts']} раз. Пропускаю.")
                    specific_task = None
                    continue
                log("Пауза 30 секунд перед повтором...")
                time.sleep(30)

            else:
                # Фиксируем успех ПЕРВЫМ делом — до любых log() которые могут упасть
                clear_session_state()
                state["completed"][task.id] = {
                    "completed_at": datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
                    "duration": duration,
                    "cost_usd": cost,
                }
                state["in_progress"] = None
                if task.id in state.get("failed", {}):
                    del state["failed"][task.id]
                save_state(state)
                total_cost += cost
                log(f"{task.id} завершена. ({duration}, ${cost:.4f})")
                specific_task = None

        except KeyboardInterrupt:
            log("Прервано пользователем (Ctrl+C). Останавливаюсь.")
            break
        except Exception:
            log("!!! Необработанное исключение в цикле:")
            for line in traceback.format_exc().splitlines():
                log(f"    {line}")
            try:
                st = load_state()
                if st.get("in_progress"):
                    log(f"  Сбрасываю in_progress={st['in_progress']!r} → None")
                    st["in_progress"] = None
                    save_state(st)
            except Exception as e2:
                log(f"  Не удалось сбросить in_progress: {e2!r}")
            log("Пауза 10 секунд перед продолжением цикла...")
            time.sleep(10)
            continue

    if all_done:
        set_jobstatus(
            "завершено",
            f"все задачи ({len(tasks)}), стоимость: ${total_cost:.4f}",
        )
    else:
        set_jobstatus(
            "остановлен",
            f"выполнено: {task_count}, стоимость: ${total_cost:.4f}",
        )
    log("")
    log(f"Цикл завершён. Выполнено задач: {task_count}. Стоимость: ${total_cost:.4f}")


# ─── Reset ────────────────────────────────────────────────────────

def reset_task(task_id: str):
    state = load_state()
    changed = False
    if task_id in state.get("completed", {}):
        del state["completed"][task_id]
        changed = True
        print(f"{task_id} сброшена (была выполнена).")
    if state.get("in_progress") == task_id:
        state["in_progress"] = None
        changed = True
        print(f"{task_id} сброшена (была в работе).")
    if task_id in state.get("failed", {}):
        del state["failed"][task_id]
        changed = True
    if changed:
        save_state(state)
    else:
        print(f"{task_id} и так не выполнена.")


# ─── Entry point ──────────────────────────────────────────────────

def main():
    alias_help = ", ".join(f"{a}->{full}" for a, full in MODEL_ALIASES.items())

    parser = argparse.ArgumentParser(
        description="Оркестратор задач Claude Manager — автозапуск сессий Claude Code.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""Примеры:
  python scripts/orchestrator.py                          # с первой доступной задачи
  python scripts/orchestrator.py --max-tasks 3            # максимум 3 задачи
  python scripts/orchestrator.py --task TASK-05           # начать с конкретной
  python scripts/orchestrator.py --task HARNESS-01        # начать с харнес-задачи
  python scripts/orchestrator.py --model haiku            # стартовать на Haiku
  python scripts/orchestrator.py --fallback-model haiku   # резерв при rate limit
  python scripts/orchestrator.py --new                    # без --resume (свежий старт)
  python scripts/orchestrator.py --stop                   # мягкая остановка
  python scripts/orchestrator.py --status                 # статус задач
  python scripts/orchestrator.py --reset TASK-05          # сбросить задачу
  python scripts/orchestrator.py --reset HARNESS-01       # сбросить харнес-задачу
  python scripts/orchestrator.py --reset-all              # сбросить всё""",
    )
    parser.add_argument("--max-tasks", type=int, default=0,
                        help="Максимум задач (0 = без ограничения)")
    parser.add_argument("--task", type=str, metavar="TASK-XX|HARNESS-XX",
                        help="Начать с конкретной задачи (напр. TASK-05 или HARNESS-01)")
    parser.add_argument("--model", type=str, default=None, metavar="MODEL",
                        help=f"Стартовая модель. Алиасы: {alias_help}. "
                             f"Также через env {INITIAL_MODEL_ENV}.")
    parser.add_argument("--fallback-model", type=str, default=None, metavar="MODEL",
                        help=f"Резервная модель при rate limit (отключает интерактивный prompt). "
                             f"Алиасы: {alias_help}. "
                             f"Также через env {FALLBACK_MODEL_ENV}.")
    parser.add_argument("--new", action="store_true",
                        help="Удалить .session.json и стартовать без --resume")
    parser.add_argument("--stop", action="store_true",
                        help="Мягкая остановка после текущей задачи")
    parser.add_argument("--status", action="store_true",
                        help="Показать статус всех задач")
    parser.add_argument("--reset", type=str, metavar="TASK-XX|HARNESS-XX",
                        help="Сбросить задачу (пометить как невыполненную)")
    parser.add_argument("--reset-all", action="store_true",
                        help="Сбросить все задачи")

    args = parser.parse_args()

    if not TASKS_FILE.exists():
        print(f"Файл задач не найден: {TASKS_FILE}")
        sys.exit(1)

    tasks = parse_tasks()
    if not tasks:
        print("Задачи не найдены в TASKS.md")
        sys.exit(1)

    state = load_state()

    if args.status:
        show_status(tasks, state)
        return

    if args.stop:
        STOP_FILE.touch()
        print(f"Оркестратор будет остановлен после текущей задачи.")
        print(f"Стоп-файл: {STOP_FILE}")
        return

    if args.reset_all:
        STATE_FILE.unlink(missing_ok=True)
        clear_session_state()
        print("Все задачи сброшены.")
        return

    if args.reset:
        reset_task(args.reset)
        return

    # Приоритет: CLI > env > None
    initial_model = resolve_model_alias(args.model) or resolve_model_alias(
        os.environ.get(INITIAL_MODEL_ENV)
    )
    fallback_model_preset = resolve_model_alias(args.fallback_model)

    run_task_loop(
        tasks,
        args.max_tasks,
        args.task,
        fallback_model_preset,
        args.new,
        initial_model,
    )


if __name__ == "__main__":
    main()
