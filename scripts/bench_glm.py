#!/usr/bin/env python3
"""GLM A/B benchmark: arm "zcode" (zcode app-server) vs arm "cm" (Claude Code
under claude-manager, provider "Claude GLM", model glm-5.3, effort high).

Both arms run the SAME tasks in the SAME sandbox directory (cwd parity);
the sandbox is wiped and re-seeded from the task starter before each run.

Usage:
  python bench_glm.py --setup                     # manager up + project registered
  python bench_glm.py --smoke                     # t1 x both arms x 1 rep
  python bench_glm.py --run --arms cm,zcode --tasks all --reps 2
  python bench_glm.py --report

Metrics land in benchmarks/glmlab/runs/results.csv; transcripts in
benchmarks/glmlab/runs/<arm>-<task>-r<rep>.json.
"""
from __future__ import annotations

import argparse
import csv
import glob
import json
import os
import shutil
import statistics
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
sys.stderr.reconfigure(encoding="utf-8")

REPO = Path(__file__).resolve().parents[1]
LAB = REPO / "benchmarks" / "glmlab"
TASKS_DIR = LAB / "tasks"
SANDBOX = LAB / "sandbox"
RUNS_DIR = LAB / "runs"
CONTROL_JSON = Path.home() / ".claude-manager" / "control.json"
GLOBAL_TOML = Path.home() / ".claude-manager" / "config.toml"
MANAGER_EXE = REPO / "build" / "bin" / "claude-manager.exe"
PY3 = Path(r"C:\Users\konstantin\AppData\Local\Python\pythoncore-3.14-64\python.exe")

PROJECT = "GLM Lab"
SESSION = "bench"
SID = f"{PROJECT}/{SESSION}"
EFFORT = "high"

# model id -> (cm model id, zcode providerId, zcode modelId)
MODELS = {
    "glm-5.3": ("glm-5.3", "account:zai-individual-coding-plan", "GLM-5.3"),
    "glm-5.3-flash": ("glm-5.3-flash", "account:zai-start-plan", "GLM-5.3-Flash"),
}
CM_MODEL, ZC_PROVIDER, ZC_MODEL = MODELS["glm-5.3"]
ZC_EXE = Path(os.environ.get("LOCALAPPDATA", "")) / "Programs" / "ZCode" / "ZCode.exe"
ZC_CJS = ZC_EXE.parent / "resources" / "glm" / "zcode.cjs"

RUN_TIMEOUT_S = 600          # per-run wall budget
CALL_TIMEOUT_S = 120         # per JSON-RPC call
MAX_TOTAL_TOKENS = 200_000_000

CSV_FIELDS = ["ts", "arm", "task", "rep", "status", "verify_pass", "wall_s",
              "turns", "tool_calls", "tool_failures", "in_tokens", "out_tokens",
              "cache_read", "cache_write", "reasoning_tokens", "cost_usd",
              "model", "effort", "notes"]

log_lock = threading.Lock()


def log(msg: str) -> None:
    line = time.strftime("%H:%M:%S") + " " + msg
    with log_lock:
        print(line, flush=True)
        RUNS_DIR.mkdir(parents=True, exist_ok=True)
        with open(RUNS_DIR / "bench.log", "a", encoding="utf-8") as f:
            f.write(line + "\n")


# ---------------------------------------------------------------- key lookup

def coding_plan_key() -> str:
    key = os.environ.get("ZAI_API_KEY", "").strip()
    if key:
        return key
    try:
        import tomllib
        with open(GLOBAL_TOML, "rb") as f:
            cfg = tomllib.load(f)
        return str(cfg.get("settings", {}).get("zcode_api_key", "")).strip()
    except Exception:
        return ""


# ---------------------------------------------------------------- cm client

class Cm:
    def __init__(self) -> None:
        self.addr: str | None = None
        self.token: str | None = None

    def load_endpoint(self) -> bool:
        try:
            data = json.loads(CONTROL_JSON.read_text(encoding="utf-8"))
            self.addr, self.token = data["addr"].rstrip("/"), data["token"]
            return True
        except Exception:
            return False

    def _post(self, path: str, payload: dict, timeout: int) -> tuple[int, dict]:
        req = urllib.request.Request(
            self.addr + path,
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json",
                     "X-CM-Token": self.token or "",
                     "X-CM-Level": "admin"},  # local harness: explicit per docs (access.go)
            method="POST")
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return r.status, json.loads(r.read().decode("utf-8"))
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", "replace")
            try:
                return e.code, json.loads(body)
            except Exception:
                return e.code, {"raw": body[:500]}

    def call(self, method: str, params: dict | None = None) -> dict:
        code, body = self._post("/rpc", {"jsonrpc": "2.0", "id": 1,
                                         "method": method, "params": params or {}}, 30)
        if code != 200 or "error" in body:
            raise RuntimeError(f"{method}: HTTP {code} {str(body)[:400]}")
        return body.get("result")

    def wait(self, body: dict) -> dict | None:
        """POST /wait; returns waitResponse dict on match, None on timeout."""
        code, resp = self._post("/wait", body, max(60, body.get("timeout_ms", 5000) // 1000 + 10))
        if code == 200:
            return resp
        if code == 408:
            return None
        raise RuntimeError(f"/wait: HTTP {code} {str(resp)[:300]}")


def ensure_manager(cm: Cm) -> None:
    if cm.load_endpoint():
        try:
            cm.call("GetProjects")
            log("manager: уже запущен, control OK")
            return
        except Exception as e:
            log(f"manager: endpoint есть, но RPC падает ({e}); пробуем всё равно")
            return
    if not MANAGER_EXE.exists():
        raise SystemExit(f"нет {MANAGER_EXE} — соберите менеджер")
    log("manager: запускаю " + str(MANAGER_EXE))
    subprocess.Popen([str(MANAGER_EXE)], cwd=str(REPO), close_fds=True)
    deadline = time.time() + 120
    while time.time() < deadline:
        if cm.load_endpoint():
            try:
                cm.call("GetProjects")
                log("manager: поднялся, control OK")
                return
            except Exception:
                pass
        time.sleep(2)
    raise SystemExit("manager не поднялся за 120 с (нет control.json/RPC)")


def ensure_project(cm: Cm) -> None:
    SANDBOX.mkdir(parents=True, exist_ok=True)
    proj_cfg_dir = SANDBOX / ".claude-manager"
    proj_cfg_dir.mkdir(exist_ok=True)
    cfg = proj_cfg_dir / "config.toml"
    if not cfg.exists():
        cfg.write_text(
            '[[session]]\nname = "bench"\nmodel = "glm-5.3"\n'
            'effort = "high"\npermission_mode = "bypassPermissions"\n',
            encoding="utf-8")
    projects = cm.call("GetProjects")
    items = projects if isinstance(projects, list) else projects.get("projects") or projects.get("items") or []
    paths = []
    for p in items:
        if isinstance(p, dict):
            paths.append(str(p.get("path") or p.get("Path") or ""))
    if str(SANDBOX).lower() not in [p.lower() for p in paths if p]:
        cm.call("AddProject", {"name": PROJECT, "path": str(SANDBOX)})
        log(f"project {PROJECT!r} зарегистрирован: {SANDBOX}")
    else:
        log(f"project {PROJECT!r} уже зарегистрирован")


# ---------------------------------------------------------------- zcode client

# Verbatim account registry push (mirrors slim-for-kimi zcode_appserver.go,
# zcodeAccountRevision; payload tied to the installed ZCode build).
ZC_REVISION = ('account:["zcode-builtin:43:3db2e911c98edc61d04c1d83ca3bbeb5f96c0c76bf1332b02aa8b67f88935374",'
               '[{"providerId":"account:zai-individual-coding-plan","config":{"access":{"type":"zhipu-account","entitled":true}}},'
               '{"providerId":"account:zai-team-coding-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},'
               '{"providerId":"account:zai-start-plan","config":{"access":{"type":"zhipu-account","entitled":true},"builtinModelIds":["GLM-5.3-Flash"]}},'
               '{"providerId":"account:bigmodel-individual-coding-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},'
               '{"providerId":"account:bigmodel-team-coding-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},'
               '{"providerId":"account:bigmodel-start-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},'
               '{"providerId":"account:zai-offpeak-idle-plan","config":{"access":{"type":"zhipu-account","entitled":true}}},'
               '{"providerId":"account:bigmodel-offpeak-idle-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},'
               '{"providerId":"account:zai-highspeed-card","config":{"access":{"type":"zhipu-account","entitled":true}}},'
               '{"providerId":"account:bigmodel-highspeed-card","config":{"access":{"type":"zhipu-account","entitled":false}}}],'
               '{"account:zai-individual-coding-plan":{"availability":"available","entitled":true,"current":true,"connectionKey":"7234ee9645a3fbdbe9aa450aacede2d8cf9761d91ba402412c79a58fe0d80b9b"},'
               '"account:zai-team-coding-plan":{"availability":"unknown","entitled":false,"current":false,"connectionKey":"7234ee9645a3fbdbe9aa450aacede2d8cf9761d91ba402412c79a58fe0d80b9b"},'
               '"account:zai-start-plan":{"availability":"available","entitled":true,"current":true,"connectionKey":"96a664e80b970a563681983c1aff0002cd7c8755ac3b3006cfe43eae9e99043e"},'
               '"account:bigmodel-individual-coding-plan":{"availability":"unavailable","entitled":false,"unavailableReason":"not-connected","current":false,"connectionKey":"145736c8450a04bf4ba21aee47794f53d87ea79c5dc4cc5b9f27fbf5b6038d53"},'
               '"account:bigmodel-team-coding-plan":{"availability":"unavailable","entitled":false,"unavailableReason":"not-connected","current":false,"connectionKey":"145736c8450a04bf4ba21aee47794f53d87ea79c5dc4cc5b9f27fbf5b6038d53"},'
               '"account:bigmodel-start-plan":{"availability":"unavailable","entitled":false,"unavailableReason":"not-connected","current":false,"connectionKey":"1166de2fcda0bb10f88164a63caadb0a0ec8611edcf75b35a9924684f9ba84bd"},'
               '"account:zai-offpeak-idle-plan":{"availability":"available","entitled":true},'
               '"account:bigmodel-offpeak-idle-plan":{"availability":"unavailable","entitled":false},'
               '"account:zai-highspeed-card":{"availability":"available","entitled":true},'
               '"account:bigmodel-highspeed-card":{"availability":"unavailable","entitled":false}}]')


def _registry() -> tuple[str, dict, dict, dict]:
    tuple3 = json.loads(ZC_REVISION[len("account:"):])
    builtin_rev = tuple3[0]
    providers = {p["providerId"]: p["config"] for p in tuple3[1]}
    states_raw = tuple3[2]
    return builtin_rev, providers, states_raw, states_raw


def zcode_builtin_config() -> str:
    root = Path.home() / ".zcode" / "v2" / "runtime" / "provider" / "windows-x86_64"
    best = ""
    if root.exists():
        for v in sorted((d.name, d) for d in root.iterdir() if d.is_dir()):
            name = v[0]
            if name.startswith("0.0.0"):
                continue
            for ep in (root / name).glob("endpoint-*/zcode-builtin.json"):
                if ep.is_file():
                    best = str(ep)
    if best:
        return best
    fallback = ZC_EXE.parent / "resources" / "config" / "provider" / "zcode-builtin.json"
    return str(fallback) if fallback.exists() else ""


class ZcodeConn:
    """NDJSON JSON-RPC client for `zcode app-server --stdio`."""

    def __init__(self, cwd: Path, api_key: str) -> None:
        self.api_key = api_key
        _, self.providers, self.states_raw, self.states = _registry()
        env = dict(os.environ)
        env["ELECTRON_RUN_AS_NODE"] = "1"
        if (p := zcode_builtin_config()):
            env.setdefault("ZCODE_BUILTIN_PROVIDER_CONFIG_FILE", p)
        pcfg = Path.home() / ".zcode" / "v2" / "provider_config.json"
        if pcfg.exists():
            env.setdefault("ZCODE_PERSONAL_PROVIDER_CONFIG_FILE", str(pcfg))
        flags = 0x08000000  # CREATE_NO_WINDOW
        self.proc = subprocess.Popen(
            [str(ZC_EXE), str(ZC_CJS), "app-server", "--stdio"],
            cwd=str(cwd), env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, creationflags=flags)
        self._next_id = 0
        self._pending: dict[int, dict] = {}
        self._cv = threading.Condition()
        self._send_lock = threading.Lock()
        self.events: list[dict] = []
        self.dead: str | None = None
        self._stderr_tail = ""
        self._reader = threading.Thread(target=self._loop, daemon=True)
        self._reader.start()
        self._err_reader = threading.Thread(target=self._read_stderr, daemon=True)
        self._err_reader.start()

    def _read_stderr(self) -> None:
        assert self.proc.stderr is not None
        for chunk in iter(lambda: self.proc.stderr.read(4096), b""):
            self._stderr_tail = (self._stderr_tail + chunk.decode("utf-8", "replace"))[-4096:]

    def _loop(self) -> None:
        assert self.proc.stdout is not None
        for line in iter(self.proc.stdout.readline, b""):
            if not line.strip():
                continue
            try:
                msg = json.loads(line)
            except Exception:
                continue
            if "method" in msg and msg.get("id") not in (None, ""):
                self._answer(str(msg["id"]), msg["method"], msg.get("params") or {})
            elif "method" in msg:
                if msg["method"] == "session/event":
                    ev = msg.get("params") or {}
                    with self._cv:
                        self.events.append(ev)
                        self._cv.notify_all()
            else:
                try:
                    rid = int(msg.get("id"))
                except (TypeError, ValueError):
                    continue
                with self._cv:
                    self._pending[rid] = msg
                    self._cv.notify_all()
        with self._cv:
            self.dead = self._stderr_tail[-300:] or "EOF"
            self._cv.notify_all()

    def _answer(self, rid: str, method: str, params: dict) -> None:
        if method == "session/requestRuntimePreferences":
            result: dict = {"nativeSearchEnhancementsEnabled": False}
        elif method == "interaction/requestProviderRuntimeHeaders":
            scope = params.get("expectedAccountScope") or ""
            if not scope:
                st = self.states.get(params.get("providerId") or "", {})
                scope = st.get("connectionKey", "")
            result = {"headersApplied": True,
                      "requestAuth": {"apiKey": self.api_key, "accountScope": scope}} \
                if self.api_key else {"headersApplied": False}
        elif method == "interaction/requestOfficialMcpAuthHeaders":
            result = {"headersApplied": False}
        elif method == "interaction/requestPermission":
            result = {"decision": "deny", "reason": "bench: no permission client"}
        else:
            result = {}
        self._send({"id": rid, "result": result})

    def _send(self, obj: dict) -> None:
        assert self.proc.stdin is not None
        with self._send_lock:
            self.proc.stdin.write(json.dumps(obj).encode("utf-8") + b"\n")
            self.proc.stdin.flush()

    def call(self, method: str, params: dict | None = None, timeout: int = CALL_TIMEOUT_S) -> dict:
        with self._cv:
            self._next_id += 1
            rid = self._next_id
        self._send({"id": rid, "method": method, **({"params": params} if params is not None else {})})
        deadline = time.time() + timeout
        with self._cv:
            while rid not in self._pending:
                rem = deadline - time.time()
                if rem <= 0 or self.dead:
                    raise RuntimeError(f"{method}: timeout/dead({self.dead})")
                self._cv.wait(rem)
            resp = self._pending.pop(rid)
        if "error" in resp:
            raise RuntimeError(f"{method}: {str(resp['error'])[:300]}")
        return resp.get("result") or {}

    def wait_terminal(self, deadline: float) -> str:
        """Block until turn.completed / turn.failed / death / deadline.
        Returns 'completed' | 'failed' | 'dead' | 'timeout'."""
        with self._cv:
            while True:
                for ev in self.events:
                    if ev.get("type") in ("turn.completed", "turn.failed"):
                        return ev["type"].split(".")[1]
                if self.dead:
                    return "dead"
                rem = deadline - time.time()
                if rem <= 0:
                    return "timeout"
                self._cv.wait(min(rem, 5))

    def close(self) -> None:
        try:
            self.proc.kill()
            self.proc.wait(timeout=10)
        except Exception:
            pass


# ---------------------------------------------------------------- tasks

def discover_tasks() -> list[str]:
    return sorted(p.name for p in TASKS_DIR.iterdir() if (p / "task.md").exists())


def prepare_sandbox(task: str) -> str:
    task_dir = TASKS_DIR / task
    SANDBOX.mkdir(parents=True, exist_ok=True)
    for child in SANDBOX.iterdir():
        if child.name == ".claude-manager":
            continue
        if child.is_dir() and not child.is_symlink():
            shutil.rmtree(child, ignore_errors=True)
        else:
            child.unlink(missing_ok=True)
    for item in (task_dir / "starter").iterdir():
        dst = SANDBOX / item.name
        if item.is_dir():
            shutil.copytree(item, dst)
        else:
            shutil.copy2(item, dst)
    # fresh standalone git repo (agents may commit; also isolates from parent repo)
    subprocess.run(["git", "init", "-q"], cwd=SANDBOX, capture_output=True)
    subprocess.run(["git", "add", "-A"], cwd=SANDBOX, capture_output=True)
    subprocess.run(["git", "-c", "user.email=bench@local", "-c", "user.name=bench",
                    "commit", "-qm", "starter"], cwd=SANDBOX, capture_output=True)
    return (task_dir / "task.md").read_text(encoding="utf-8").strip()


def verify(task: str) -> tuple[bool, str]:
    vpy = TASKS_DIR / task / "verify.py"
    try:
        r = subprocess.run([str(PY3), str(vpy), str(SANDBOX)], cwd=SANDBOX,
                           capture_output=True, timeout=600)
        out = (r.stdout + r.stderr).decode("utf-8", "replace").strip()
        tail = " | ".join(out.splitlines()[-3:])[:400]
        return r.returncode == 0, tail
    except subprocess.TimeoutExpired:
        return False, "verify timeout"


# ---------------------------------------------------------------- arms

def run_cm(cm: Cm, task: str, rep: int, prompt: str, model: str = CM_MODEL) -> dict:
    t0 = time.time()
    row = {"arm": "cm", "task": task, "rep": rep, "model": model, "effort": EFFORT}
    err_events = 0
    status = "completed"
    finish: dict = {}
    try:
        # best-effort cleanup of a leftover run, then a fresh start
        try:
            cm.call("StopSession", {"id": SID})
        except Exception:
            pass
        for _ in range(30):  # stop is async: wait until not running
            try:
                ov = cm.call("GetSessionOverview", {"id": SID})
                st = (ov.get("session") or {}).get("status", "")
                if st not in ("working", "starting", "waiting_permission"):
                    break
            except Exception:
                break
            time.sleep(2)
        try:
            cm.call("ClearSessionState", {"project": PROJECT, "session": SESSION})
        except Exception:
            pass
        cm.call("StartSessionWithModel", {"Project": PROJECT, "Session": SESSION,
                                          "Model": model, "Effort": EFFORT})
        # status=working arrives immediately (input is queued until the CLI
        # is ready — Session.queueInput buffers), so plain wait is enough
        w = cm.wait({"event": "session:status",
                     "match": {"id": SID, "status": "working"},
                     "timeout_ms": 30000})
        if w is None:
            raise RuntimeError("session never reached status=working")
        sent = False
        for _ in range(5):
            try:
                cm.call("SendMessage", {"id": SID, "message": prompt})
                sent = True
                break
            except RuntimeError as e:
                if "not active" in str(e):
                    time.sleep(2)
                    continue
                raise
        if not sent:
            raise RuntimeError("SendMessage rejected repeatedly")

        deadline = time.time() + RUN_TIMEOUT_S
        while time.time() < deadline:
            remaining_ms = int(min(540_000, (deadline - time.time()) * 1000))
            w = cm.wait({"attention": [SID], "timeout_ms": remaining_ms})
            if w is None:
                continue
            name = w.get("event", "")
            data = w.get("data") or {}
            if name == "session:result":
                finish = data.get("result") or {}
                break
            if name == "session:task_done":
                continue  # result follows with the authoritative usage
            if name == "session:error":
                err_events += 1
                continue
            if name in ("session:question", "session:permission"):
                status = "stuck_needs_attention"
                break
        if not finish and status == "completed":
            status = "timeout"
        row["wall_s"] = round(time.time() - t0, 1)
    except Exception as e:
        row["status"] = f"error: {str(e)[:200]}"
        row["wall_s"] = round(time.time() - t0, 1)

    # stop FIRST so the run row finalizes, then collect metrics/transcript
    try:
        cm.call("StopSession", {"id": SID})
    except Exception as e:
        log(f"StopSession: {e}")
    for _ in range(30):
        try:
            ov = cm.call("GetSessionOverview", {"id": SID})
            st = (ov.get("session") or {}).get("status", "")
            if st not in ("working", "starting", "waiting_permission"):
                break
        except Exception:
            break
        time.sleep(2)

    usage = finish.get("usage") or {}

    # metrics + transcript
    run_row: dict = {}
    log_entries: list = []
    try:
        hist = cm.call("GetHistory", {"project": PROJECT, "limit": 5})
        runs = hist if isinstance(hist, list) else hist.get("runs") or hist.get("history") or []
        if runs:
            best = max(runs, key=lambda r: str(r.get("StartedAt") or r.get("started_at") or ""))
            run_row = best if isinstance(best, dict) else {}
    except Exception as e:
        log(f"GetHistory failed: {e}")
    try:
        lg = cm.call("GetSessionLog", {"id": SID, "offset": 0, "limit": 20000})
        log_entries = lg if isinstance(lg, list) else lg.get("entries") or lg.get("log") or []
    except Exception as e:
        log(f"GetSessionLog failed: {e}")

    tools = sum(1 for e in log_entries if isinstance(e, dict) and e.get("Level") == "tool")
    tool_err = sum(1 for e in log_entries if isinstance(e, dict) and e.get("Level") == "error")

    if "status" not in row:
        row["status"] = status
    if err_events:
        row["notes"] = f"error_events={err_events}"
    row["turns"] = finish.get("num_turns") or run_row.get("NumTurns") or 0
    row["tool_calls"] = tools
    row["tool_failures"] = tool_err
    row["in_tokens"] = usage.get("input_tokens") or run_row.get("InputTokens") or 0
    row["out_tokens"] = usage.get("output_tokens") or run_row.get("OutputTokens") or 0
    row["cache_read"] = (usage.get("cache_read_input_tokens")
                         or run_row.get("CacheReadTokens") or 0)
    row["cache_write"] = (usage.get("cache_creation_input_tokens")
                          or run_row.get("CacheCreationTokens") or 0)
    row["reasoning_tokens"] = ""
    row["cost_usd"] = finish.get("total_cost_usd") or run_row.get("TotalCostUSD") or 0.0

    transcript = {"meta": row, "run": run_row,
                  "log": log_entries[-400:]}  # tail to keep files small
    (RUNS_DIR / f"cm-{task}-r{rep}.json").write_text(
        json.dumps(transcript, ensure_ascii=False, indent=1), encoding="utf-8")

    # persisted conversation state must not leak into the next run; the
    # session itself was already stopped before metrics collection
    try:
        cm.call("ClearSessionState", {"project": PROJECT, "session": SESSION})
    except Exception as e:
        log(f"cleanup ClearSessionState: {e}")
    return row


def run_zcode(task: str, rep: int, prompt: str, api_key: str,
              zc_model: str = ZC_MODEL) -> dict:
    t0 = time.time()
    row = {"arm": "zcode", "task": task, "rep": rep, "model": zc_model, "effort": EFFORT}
    conn: ZcodeConn | None = None
    status = "completed"
    err_msg = ""
    try:
        conn = ZcodeConn(SANDBOX, api_key)
        builtin_rev, providers, states_raw, states = _registry()
        conn.call("provider/updateAccountConfig", {
            "revision": ZC_REVISION,
            "basedOnZCodeBuiltinRevision": builtin_rev,
            "providers": providers,
            "states": states_raw,
        })
        provider = ""
        if states.get(ZC_PROVIDER, {}).get("entitled"):
            provider = ZC_PROVIDER
        else:
            provider = next((pid for pid, st in states.items()
                             if st.get("entitled") and pid.startswith("account:zai-")), "")
        if not provider:
            raise RuntimeError("нет entitled z.ai провайдера в реестре")
        created = conn.call("session/create", {
            "workspace": {"workspacePath": str(SANDBOX), "workspaceKey": str(SANDBOX)},
            "mode": "yolo"})
        session_id = created.get("sessionId") or created.get("session", {}).get("sessionId") or ""
        if not session_id:
            raise RuntimeError("session/create вернул пустой sessionId")
        conn.call("session/subscribe", {"sessionId": session_id,
                                        "deliveryKind": "desktop-continuous"})
        sel = {"providerId": provider, "modelId": zc_model}
        if EFFORT:
            sel["options"] = {"reasoningLevel": EFFORT}
        conn.call("session/send", {"sessionId": session_id, "content": prompt,
                                   "modelSelection": sel})
        deadline = time.time() + RUN_TIMEOUT_S
        outcome = conn.wait_terminal(deadline)
        if outcome == "failed":
            status = "turn_failed"
        elif outcome == "dead":
            status = "child_died"
        elif outcome == "timeout":
            status = "timeout"
    except Exception as e:
        status, err_msg = f"error: {str(e)[:200]}", str(e)[:300]
    row["status"] = status
    row["wall_s"] = round(time.time() - t0, 1)

    # aggregate from events
    usage_final: dict = {}
    usage_sum = {"inputTokens": 0, "outputTokens": 0, "reasoningTokens": 0,
                 "cacheReadTokens": 0, "cacheWriteTokens": 0}
    tool_calls = tool_fail = snapshots = 0
    ev_digest: list[dict] = []
    if conn:
        for ev in conn.events:
            et = ev.get("type")
            payload = ev.get("payload") or {}
            if et == "model.streaming":
                if payload.get("kind") == "tool_call":
                    tool_calls += 1
                    ev_digest.append({"t": et, "tool": payload.get("toolName", "")})
            elif et == "tool.updated":
                if payload.get("kind") == "result":
                    res = payload.get("result") or {}
                    if not res.get("success", True):
                        tool_fail += 1
                        ev_digest.append({"t": "tool_failed", "tool": payload.get("toolName", ""),
                                          "err": str(res.get("error", ""))[:150]})
            elif et == "session.updated":
                if payload.get("usage"):
                    snapshots += 1
                    for k in usage_sum:
                        usage_sum[k] += int(payload["usage"].get(k) or 0)
            elif et == "turn.completed":
                usage_final = payload.get("usage") or {}
            elif et == "turn.failed":
                err_msg = str((payload.get("error") or {}).get("message", ""))[:300]
                ev_digest.append({"t": "turn.failed", "err": err_msg})
        conn.close()

    fin = {k: int(usage_final.get(k) or 0) for k in usage_sum}
    row["turns"] = snapshots
    row["tool_calls"] = tool_calls
    row["tool_failures"] = tool_fail
    row["in_tokens"] = fin["inputTokens"] or usage_sum["inputTokens"]
    row["out_tokens"] = fin["outputTokens"] or usage_sum["outputTokens"]
    row["cache_read"] = fin["cacheReadTokens"] or usage_sum["cacheReadTokens"]
    row["cache_write"] = fin["cacheWriteTokens"] or usage_sum["cacheWriteTokens"]
    row["reasoning_tokens"] = fin["reasoningTokens"] or usage_sum["reasoningTokens"]
    row["cost_usd"] = ""
    if err_msg and status != "completed":
        row["notes"] = err_msg[:200]
    transcript = {"meta": row, "events_digest": ev_digest,
                  "final_usage": usage_final, "sum_snapshots": usage_sum}
    (RUNS_DIR / f"zcode-{task}-r{rep}.json").write_text(
        json.dumps(transcript, ensure_ascii=False, indent=1), encoding="utf-8")
    return row


# ---------------------------------------------------------------- orchestration

def used_tokens_total() -> int:
    total = 0
    csv_path = RUNS_DIR / "results.csv"
    if csv_path.exists():
        with open(csv_path, newline="", encoding="utf-8") as f:
            for r in csv.DictReader(f):
                total += sum(int(r.get(k) or 0) for k in
                             ("in_tokens", "out_tokens", "cache_read", "cache_write"))
    return total


def append_csv(row: dict) -> None:
    RUNS_DIR.mkdir(parents=True, exist_ok=True)
    csv_path = RUNS_DIR / "results.csv"
    new = not csv_path.exists()
    with open(csv_path, "a", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=CSV_FIELDS)
        if new:
            w.writeheader()
        row = {"ts": time.strftime("%Y-%m-%d %H:%M:%S"), **row}
        w.writerow({k: row.get(k, "") for k in CSV_FIELDS})


def cmd_run(arms: list[str], tasks: list[str], reps: int,
            model: str = "glm-5.3", rep_offset: int = 0) -> None:
    key = coding_plan_key()
    if not key:
        raise SystemExit("не найден API-ключ (ZAI_API_KEY / zcode_api_key в ~/.claude-manager/config.toml)")
    cm_model, zc_provider, zc_model = MODELS[model]
    globals()["ZC_PROVIDER"] = zc_provider
    cm = Cm()
    ensure_manager(cm)
    ensure_project(cm)
    if "all" in tasks:
        tasks = discover_tasks()
    log(f"матрица: arms={arms} tasks={tasks} reps={reps} offset={rep_offset} model={model}")
    for rep in range(rep_offset + 1, rep_offset + reps + 1):
        for task in tasks:
            for arm in arms:
                if used_tokens_total() > MAX_TOTAL_TOKENS:
                    raise SystemExit("достигнут лимит суммарных токенов — стоп")
                prompt = prepare_sandbox(task)
                log(f"=== {arm} {task} rep{rep}: старт")
                if arm == "cm":
                    row = run_cm(cm, task, rep, prompt, cm_model)
                else:
                    row = run_zcode(task, rep, prompt, key, zc_model)
                vok, vmsg = verify(task)
                row["verify_pass"] = "PASS" if vok else "FAIL"
                if not vok and row["status"] == "completed":
                    row["status"] += "+verify_fail"
                log(f"=== {arm} {task} rep{rep}: {row['status']} verify={row['verify_pass']} "
                    f"wall={row['wall_s']}s turns={row.get('turns')} in={row.get('in_tokens')} "
                    f"out={row.get('out_tokens')} cacheR={row.get('cache_read')}")
                append_csv(row)


def cmd_report() -> None:
    csv_path = RUNS_DIR / "results.csv"
    rows = list(csv.DictReader(open(csv_path, newline="", encoding="utf-8"))) \
        if csv_path.exists() else []
    if not rows:
        raise SystemExit("нет результатов в results.csv")
    lines = [f"# GLM A/B отчёт — {time.strftime('%Y-%m-%d %H:%M')}", "",
             "Плечо cm = Claude Code через claude-manager (Claude GLM, glm-5.3/high);",
             "плечо zcode = ZCode app-server (GLM-5.3, reasoningLevel high).", ""]
    for arm in ("cm", "zcode"):
        rs = [r for r in rows if r["arm"] == arm]
        if not rs:
            continue
        done = [r for r in rs if r["status"].startswith("completed") and "+" not in r["status"]]
        vok = [r for r in rs if r["verify_pass"] == "PASS"]
        def med(f, xs=None):
            xs = xs if xs is not None else rs
            vals = [float(r[f]) for r in xs if str(r.get(f) or "").replace(".", "", 1).isdigit()]
            return statistics.median(vals) if vals else 0
        lines += [
            f"## {arm} — {len(rs)} прогонов",
            f"- verify PASS: {len(vok)}/{len(rs)}",
            f"- полностью успешных (completed, verify PASS): {len([r for r in done if r['verify_pass']=='PASS'])}/{len(rs)}",
            f"- медиана wall: {med('wall_s'):.0f} с; ходов: {med('turns'):.0f}; tool-вызовов: {med('tool_calls'):.0f}",
            f"- медиана токенов/прогон: in {med('in_tokens'):.0f}, out {med('out_tokens'):.0f}, "
            f"cache_read {med('cache_read'):.0f}, cache_write {med('cache_write'):.0f}",
            f"- суммарно токенов: " + ", ".join(
                f"{k}={sum(int(r[k] or 0) for r in rs):,}" for k in
                ("in_tokens", "out_tokens", "cache_read", "cache_write")),
            f"- ошибок tool: {sum(int(r['tool_failures'] or 0) for r in rs)}; "
            f"прогонов с ненулевым статусом-ошибкой: {sum(1 for r in rs if not r['status'].startswith('completed'))}",
            "",
        ]
    lines.append("## По задачам")
    lines.append("| arm | task | rep | status | verify | wall | turns | in | out | cR | cW |")
    lines.append("|---|---|---|---|---|---|---|---|---|---|---|")
    for r in rows:
        lines.append(f"| {r['arm']} | {r['task']} | {r['rep']} | {r['status']} | "
                     f"{r['verify_pass']} | {r['wall_s']} | {r['turns']} | {r['in_tokens']} | "
                     f"{r['out_tokens']} | {r['cache_read']} | {r['cache_write']} |")
    out = LAB / f"report-{time.strftime('%Y-%m-%d')}.md"
    out.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print("\n".join(lines))
    print(f"\nотчёт: {out}")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--setup", action="store_true")
    ap.add_argument("--smoke", action="store_true")
    ap.add_argument("--run", action="store_true")
    ap.add_argument("--arms", default="cm,zcode")
    ap.add_argument("--tasks", default="all")
    ap.add_argument("--reps", type=int, default=1)
    ap.add_argument("--rep-offset", type=int, default=0)
    ap.add_argument("--model", default="glm-5.3", choices=list(MODELS))
    ap.add_argument("--report", action="store_true")
    a = ap.parse_args()
    RUNS_DIR.mkdir(parents=True, exist_ok=True)
    if a.setup:
        cm = Cm()
        ensure_manager(cm)
        ensure_project(cm)
        key = coding_plan_key()
        log(f"setup OK; API-ключ: {'есть' if key else 'НЕТ'}; zcode: {ZC_EXE.exists() and ZC_CJS.exists()}")
        return
    if a.smoke:
        cmd_run(a.arms.split(","), ["t1-fix-go-bug"], 1, a.model, a.rep_offset)
        return
    if a.run:
        cmd_run(a.arms.split(","), a.tasks.split(","), a.reps, a.model, a.rep_offset)
        return
    if a.report:
        cmd_report()
        return
    ap.print_help()


if __name__ == "__main__":
    main()
