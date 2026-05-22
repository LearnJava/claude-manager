#!/usr/bin/env python3
"""
Оркестратор задач Claude Manager.

Автоматический запуск сессий Claude Code для последовательного
выполнения задач из TASKS.md с учётом зависимостей.

Использование:
    python scripts/orchestrator.py                  # запуск с первой доступной задачи
    python scripts/orchestrator.py --max-tasks 3    # максимум 3 задачи, потом стоп
    python scripts/orchestrator.py --task TASK-05   # начать с конкретной задачи
    python scripts/orchestrator.py --stop           # мягкая остановка
    python scripts/orchestrator.py --status         # статус всех задач
    python scripts/orchestrator.py --reset TASK-05  # сбросить задачу
    python scripts/orchestrator.py --reset-all      # сбросить всё
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
from datetime import datetime, timedelta
from pathlib import Path

# Корень проекта — два уровня вверх от scripts/
PROJECT_DIR = Path(__file__).resolve().parent.parent
SCRIPTS_DIR = Path(__file__).resolve().parent
TASKS_FILE = PROJECT_DIR / "TASKS.md"
STATE_FILE = SCRIPTS_DIR / ".taskstate.json"
STOP_FILE = SCRIPTS_DIR / ".stop"
JOBSTATUS_FILE = SCRIPTS_DIR / ".jobstatus"


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


def parse_tasks() -> list[Task]:
    """Парсить TASKS.md — извлечь задачи, зависимости, промпты."""
    content = TASKS_FILE.read_text(encoding="utf-8")
    tasks = []

    # Разбить по заголовкам задач: ## TASK-XX: Title
    task_pattern = re.compile(r"^## (TASK-\d+):\s*(.+)$", re.MULTILINE)
    matches = list(task_pattern.finditer(content))

    for i, match in enumerate(matches):
        task_id = match.group(1)
        title = match.group(2).strip()

        # Содержимое секции — до следующей задачи или конца файла
        start = match.end()
        end = matches[i + 1].start() if i + 1 < len(matches) else len(content)
        section = content[start:end]

        # Зависимости
        dep_match = re.search(r"\*\*Depends on:\*\*\s*(.+)", section)
        depends = []
        if dep_match:
            dep_text = dep_match.group(1).strip()
            if dep_text.lower() != "nothing":
                depends = re.findall(r"TASK-\d+", dep_text)

        # Промпт (между ``` после **Prompt:**)
        prompt_match = re.search(
            r"\*\*Prompt:\*\*\s*```\s*\n(.*?)```", section, re.DOTALL
        )
        prompt = prompt_match.group(1).strip() if prompt_match else ""

        tasks.append(Task(task_id, title, depends, prompt))

    return tasks


# ─── State management ────────────────────────────────────────────

def load_state() -> dict:
    """Загрузить состояние задач."""
    if STATE_FILE.exists():
        return json.loads(STATE_FILE.read_text(encoding="utf-8"))
    return {"completed": {}, "in_progress": None, "failed": {}}


def save_state(state: dict):
    STATE_FILE.write_text(
        json.dumps(state, indent=2, ensure_ascii=False), encoding="utf-8"
    )


def get_next_task(
    tasks: list[Task], state: dict, specific_task: str = None
) -> Task | None:
    """Найти следующую задачу, у которой все зависимости выполнены."""
    completed = set(state.get("completed", {}).keys())

    # Если есть задача в процессе — продолжить её
    in_progress = state.get("in_progress")
    if in_progress:
        for task in tasks:
            if task.id == in_progress:
                return task

    # Конкретная задача запрошена
    if specific_task:
        for task in tasks:
            if task.id == specific_task:
                if task.id in completed:
                    log(f"{task.id} уже выполнена.")
                    return None
                missing = [d for d in task.depends if d not in completed]
                if missing:
                    log(
                        f"{task.id} заблокирована: не выполнены {', '.join(missing)}"
                    )
                    return None
                return task
        log(f"Задача {specific_task} не найдена.")
        return None

    # Первая задача, у которой все зависимости выполнены
    for task in tasks:
        if task.id in completed:
            continue
        if all(dep in completed for dep in task.depends):
            return task

    return None


# ─── Status display ──────────────────────────────────────────────

def show_status(tasks: list[Task], state: dict):
    completed = state.get("completed", {})
    in_progress = state.get("in_progress")
    failed = state.get("failed", {})

    print("Статус задач Claude Manager:")
    print("=" * 60)

    # Выполнено
    done_tasks = [t for t in tasks if t.id in completed]
    if done_tasks:
        print("\n  Выполнено:")
        for task in done_tasks:
            info = completed[task.id]
            dur = info.get("duration", "?")
            cost = info.get("cost_usd", 0)
            ts = info.get("completed_at", "?")
            print(f"    {task.id}: {task.title}")
            print(f"           {dur}, ${cost:.4f}, {ts}")

    # В работе
    if in_progress:
        for task in tasks:
            if task.id == in_progress:
                print(f"\n  В работе:")
                print(f"    {task.id}: {task.title}")
                break

    # Ошибки
    failed_tasks = [t for t in tasks if t.id in failed]
    if failed_tasks:
        print(f"\n  С ошибкой:")
        for task in failed_tasks:
            info = failed[task.id]
            print(
                f"    {task.id}: {task.title} (попыток: {info.get('attempts', 0)})"
            )

    # Готово к запуску (зависимости выполнены)
    ready = []
    pending = []
    for task in tasks:
        if task.id in completed or task.id == in_progress:
            continue
        if all(dep in completed for dep in task.depends):
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
            missing = [d for d in task.depends if d not in completed]
            print(f"    {task.id}: {task.title}")
            print(f"           <- {', '.join(missing)}")

    print()
    print("=" * 60)
    total = len(tasks)
    done = len(completed)
    pct = done * 100 // total if total > 0 else 0
    total_cost = sum(info.get("cost_usd", 0) for info in completed.values())
    print(f"Прогресс: {done}/{total} ({pct}%)  |  Потрачено: ${total_cost:.4f}")

    # Статус оркестратора
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
    """Форматировать tool_use блок в одну строку."""
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
    """Превратить JSON-событие stream-json в читаемые строки для лога."""
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


def run_claude(task: Task) -> tuple[int, bool, float]:
    """Запустить claude с промптом задачи. Возвращает (exit_code, rate_limited, cost_usd)."""

    full_prompt = (
        f"Read CLAUDE.md and PLAN.md for project context. "
        f"Your task is {task.id}: {task.title}.\n\n"
        f"{task.prompt}\n\n"
        f"Focus only on this task. Do not work on other tasks. "
        f"Make sure the code compiles when you're done."
    )

    process = subprocess.Popen(
        [
            "claude",
            "-p",
            full_prompt,
            "--dangerously-skip-permissions",
            "--verbose",
            "--output-format",
            "stream-json",
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=PROJECT_DIR,
        text=True,
        encoding="utf-8",
        errors="replace",
    )

    rate_limited = False
    cost_usd = 0.0

    for line in process.stdout:
        line = line.strip()
        if not line:
            continue

        # Детект rate limit в сыром тексте
        if "hit your limit" in line.lower() or "rate limit" in line.lower():
            rate_limited = True
            log(f"  Rate limit: {line[:120]}")
            continue

        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            if "hit your limit" in line.lower() or "rate limit" in line.lower():
                rate_limited = True
                log(f"  Rate limit: {line[:120]}")
            continue

        # Rate limit в JSON-событии
        if event.get("type") == "rate_limit_event":
            info = event.get("rate_limit_info", {})
            if info.get("status", "").startswith("blocked"):
                rate_limited = True
                log("  Rate limit (blocked)")

        # Стоимость из result-события
        if event.get("type") == "result":
            cost_usd = event.get("total_cost_usd", 0.0) or 0.0

        for display_line in format_event(event):
            log(display_line)

    # Проверить stderr — rate limit может быть там
    stderr_output = process.stderr.read()
    if stderr_output and (
        "hit your limit" in stderr_output.lower()
        or "rate limit" in stderr_output.lower()
    ):
        rate_limited = True
        match = RATE_LIMIT_RE.search(stderr_output)
        if match:
            log(f"  Rate limit до {match.group(1)}")
        else:
            log(f"  Rate limit обнаружен")

    process.wait()
    return process.returncode, rate_limited, cost_usd


def wait_for_rate_limit():
    """Подождать 5 минут при rate limit."""
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

def run_task_loop(tasks: list[Task], max_tasks: int = 0, specific_task: str = None):
    """Основной цикл: берём задачу → запускаем claude → отмечаем → следующая."""
    state = load_state()
    task_count = 0
    total_cost = 0.0

    log(f"Старт оркестратора. Проект: {PROJECT_DIR}")
    log(f"Задач: {len(tasks)}, выполнено: {len(state.get('completed', {}))}")
    log(f"Стоп-файл: {STOP_FILE}")
    log("")
    set_jobstatus("запущен")

    while True:
        # Проверка стоп-файла
        if STOP_FILE.exists():
            log("Найден стоп-файл. Останавливаюсь.")
            STOP_FILE.unlink()
            break

        # Проверка лимита задач
        if max_tasks > 0 and task_count >= max_tasks:
            log(f"Достигнут лимит задач ({max_tasks}). Останавливаюсь.")
            break

        # Найти следующую задачу
        state = load_state()
        task = get_next_task(tasks, state, specific_task if task_count == 0 else None)

        if task is None:
            if len(state.get("completed", {})) == len(tasks):
                log("Все задачи выполнены!")
            else:
                log("Нет доступных задач (зависимости не выполнены или все сделано).")
            break

        task_count += 1
        log(f"{'=' * 60}")
        log(f"Задача #{task_count}: {task.id} — {task.title}")
        if task.depends:
            log(f"  Зависимости: {', '.join(task.depends)}")
        log(f"{'=' * 60}")

        # Пометить как «в работе»
        state["in_progress"] = task.id
        save_state(state)
        set_jobstatus("работает", f"{task.id}: {task.title}")

        start_time = time.time()

        log("Запуск claude...")
        try:
            exit_code, rate_limited, cost = run_claude(task)
        except FileNotFoundError:
            log("claude не найден в PATH.")
            break
        except Exception as e:
            log(f"Ошибка запуска: {e}")
            break

        elapsed = time.time() - start_time
        duration = format_duration(elapsed)

        if rate_limited and exit_code != 0:
            # Rate limit — не считать как попытку, подождать и повторить
            task_count -= 1
            state["in_progress"] = None
            save_state(state)
            wait_for_rate_limit()

        elif exit_code != 0:
            # Ошибка — подсчитать попытки
            task_count -= 1
            log(f"Claude завершился с кодом {exit_code}. ({duration})")

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
            # Успех
            total_cost += cost
            log(f"{task.id} завершена. ({duration}, ${cost:.4f})")

            state["completed"][task.id] = {
                "completed_at": datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
                "duration": duration,
                "cost_usd": cost,
            }
            state["in_progress"] = None
            # Сбросить счётчик ошибок при успехе
            if task.id in state.get("failed", {}):
                del state["failed"][task.id]
            save_state(state)

            specific_task = None

    set_jobstatus("остановлен", f"выполнено: {task_count}, стоимость: ${total_cost:.4f}")
    log("")
    log(f"Цикл завершён. Выполнено задач: {task_count}. Стоимость: ${total_cost:.4f}")


# ─── Reset ────────────────────────────────────────────────────────

def reset_task(task_id: str):
    """Сбросить задачу (пометить как невыполненную)."""
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
    parser = argparse.ArgumentParser(
        description="Оркестратор задач Claude Manager — автозапуск сессий Claude Code.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""Примеры:
  python scripts/orchestrator.py                  # запуск с первой доступной задачи
  python scripts/orchestrator.py --max-tasks 3    # максимум 3 задачи
  python scripts/orchestrator.py --task TASK-05   # начать с конкретной
  python scripts/orchestrator.py --stop           # мягкая остановка
  python scripts/orchestrator.py --status         # статус задач
  python scripts/orchestrator.py --reset TASK-05  # сбросить задачу
  python scripts/orchestrator.py --reset-all      # сбросить всё""",
    )
    parser.add_argument(
        "--max-tasks",
        type=int,
        default=0,
        help="Максимум задач (0 = без ограничения)",
    )
    parser.add_argument(
        "--task",
        type=str,
        metavar="TASK-XX",
        help="Начать с конкретной задачи (напр. TASK-05)",
    )
    parser.add_argument(
        "--stop",
        action="store_true",
        help="Мягкая остановка после текущей задачи",
    )
    parser.add_argument(
        "--status",
        action="store_true",
        help="Показать статус всех задач",
    )
    parser.add_argument(
        "--reset",
        type=str,
        metavar="TASK-XX",
        help="Сбросить задачу (пометить как невыполненную)",
    )
    parser.add_argument(
        "--reset-all",
        action="store_true",
        help="Сбросить все задачи",
    )

    args = parser.parse_args()

    # Проверить наличие TASKS.md
    if not TASKS_FILE.exists():
        print(f"Файл задач не найден: {TASKS_FILE}")
        sys.exit(1)

    tasks = parse_tasks()
    if not tasks:
        print("Задачи не найдены в TASKS.md")
        sys.exit(1)

    state = load_state()

    # Режим статуса
    if args.status:
        show_status(tasks, state)
        return

    # Режим остановки
    if args.stop:
        STOP_FILE.touch()
        print(f"Оркестратор будет остановлен после текущей задачи.")
        print(f"Стоп-файл: {STOP_FILE}")
        return

    # Режим сброса
    if args.reset_all:
        STATE_FILE.unlink(missing_ok=True)
        print("Все задачи сброшены.")
        return

    if args.reset:
        reset_task(args.reset)
        return

    # Режим запуска
    run_task_loop(tasks, args.max_tasks, args.task)


if __name__ == "__main__":
    main()
