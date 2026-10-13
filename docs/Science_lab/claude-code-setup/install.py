"""Install the claude-code-setup kit for Claude Code on a Windows machine.

    python install.py --dry-run        # show what would change, change nothing
    python install.py                  # install into %USERPROFILE%\\.claude
    python install.py --home D:\\tmp\\h  # install into another home (for testing)
    python install.py --uninstall      # remove what this kit installed

What it does (idempotent; re-running replaces only its own parts):
  1. finds the real python.exe (not the WindowsApps alias) and the console OEM code page;
  2. copies the Job Object limiter to <home>\\.claude\\tools\\ (jobrun.exe, or jobrun.py if the exe fails);
  3. writes a marked section into <home>\\.claude\\CLAUDE.md (between BEGIN/END markers, rest untouched);
  4. writes the script-first skill to <home>\\.claude\\skills\\script-first\\SKILL.md;
  5. merges permission rules for the limiter into <home>\\.claude\\settings.json (keeps everything else);
  6. reports hooks that call bare `python` (slow alias) without changing them, unless --fix-hooks;
  7. verifies: limiter kills an infinite loop, Python prints UTF-8.
Every file it changes is backed up first as <file>.bak-<timestamp>.
"""
import argparse
import ctypes
import datetime
import glob
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

KIT = Path(__file__).resolve().parent
BEGIN = "<!-- BEGIN claude-code-setup -->"
END = "<!-- END claude-code-setup -->"
STAMP = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")


def log(msg):
    print(msg, flush=True)


def is_alias(p: str) -> bool:
    return "windowsapps" in p.lower()


def find_python() -> str | None:
    cands = []
    if sys.executable and not is_alias(sys.executable):
        cands.append(sys.executable)
    try:
        out = subprocess.run(["py", "-0p"], capture_output=True, text=True, timeout=20).stdout
        cands += re.findall(r"([A-Za-z]:\\[^\r\n*]+?python\.exe)", out)
    except (OSError, subprocess.SubprocessError):
        pass
    local = os.environ.get("LOCALAPPDATA", "")
    for pat in (r"Python\pythoncore-*\python.exe", r"Programs\Python\Python*\python.exe"):
        cands += sorted(glob.glob(os.path.join(local, pat)), reverse=True)
    for pf in (os.environ.get("ProgramFiles", ""), os.environ.get("ProgramFiles(x86)", "")):
        cands += sorted(glob.glob(os.path.join(pf, "Python*", "python.exe")), reverse=True)
    for c in cands:
        if c and os.path.isfile(c) and not is_alias(c):
            return os.path.normpath(c)
    return None


def oem_cp() -> int:
    try:
        return ctypes.windll.kernel32.GetOEMCP()
    except AttributeError:
        return 0


def backup(path: Path, dry: bool):
    if path.exists() and not dry:
        shutil.copy2(path, path.with_name(path.name + f".bak-{STAMP}"))


def write(path: Path, text: str, dry: bool):
    if path.exists() and path.read_text(encoding="utf-8") == text:
        log(f"  unchanged {path}")
        return
    log(f"  write {path}")
    if dry:
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    backup(path, dry)
    path.write_text(text, encoding="utf-8", newline="\n")


def run(cmd, timeout=30):
    return subprocess.run(cmd, capture_output=True, timeout=timeout)


def install_limiter(tools: Path, python: str, dry: bool) -> str:
    """Returns the command prefix that runs the limiter."""
    exe = tools / "jobrun.exe"
    log(f"[2] limiter -> {tools}")
    if not dry:
        # jobrun.exe/.py/.c, handoff_hook.py, measure/*.py
        shutil.copytree(KIT / "tools", tools, dirs_exist_ok=True,
                        ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
    if dry:
        return f"\"{exe}\""
    try:
        r = run([str(exe), "64", "10000", python, "-c", "pass"])
        if r.returncode == 0:
            log("  jobrun.exe works")
            return f"\"{exe}\""
        log(f"  jobrun.exe failed (rc={r.returncode}), using jobrun.py")
    except OSError as e:
        log(f"  jobrun.exe cannot start ({e}), using jobrun.py")
    return f"\"{python}\" \"{tools / 'jobrun.py'}\""


def render(name: str, python: str, jobrun: str, cp: int) -> str:
    t = (KIT / "templates" / name).read_text(encoding="utf-8")
    oem = (KIT / "templates" / "CLAUDE.oem-cp866.md").read_text(encoding="utf-8") if cp == 866 else ""
    return (t.replace("{{PYTHON}}", python).replace("{{JOBRUN}}", jobrun)
             .replace("{{OEM_CP}}", f"CP{cp}" if cp else "unknown").replace("{{OEM_SECTION}}", oem))


def merge_claude_md(path: Path, section: str, dry: bool, remove=False):
    old = path.read_text(encoding="utf-8") if path.exists() else ""
    block = f"{BEGIN}\n{section.rstrip()}\n{END}\n"
    if BEGIN in old and END in old:
        pre, rest = old.split(BEGIN, 1)
        post = rest.split(END, 1)[1].lstrip("\n")
        new = pre + ("" if remove else block) + post
    elif remove:
        log("  no kit section in CLAUDE.md")
        return
    else:
        new = (old.rstrip() + "\n\n" if old.strip() else "# User-level instructions\n\n") + block
    write(path, new, dry)


def limiter_rules(jobrun: str) -> list[str]:
    """Permission rules matching exactly the limiter command prefix, in the path spellings Claude Code uses.

    In the jobrun.py fallback the prefix is `python jobrun.py`: the rules must name both tokens, otherwise
    they would allow every python command."""
    tokens = [a or b for a, b in re.findall(r"\"([^\"]+)\"|(\S+)", jobrun)]

    def drive_unix(p):
        u = p.replace("\\", "/")
        return "/" + u[0].lower() + u[2:] if re.match(r"^[A-Za-z]:/", u) else u

    spellings = [
        lambda p: p,                          # D:\bench\bin\jobrun.exe
        lambda p: p.replace("\\", "/"),       # D:/bench/bin/jobrun.exe
        drive_unix,                           # /d/bench/bin/jobrun.exe
        lambda p: f"\"{p}\"",
        lambda p: f"\"{p.replace(chr(92), '/')}\"",
    ]
    return sorted({"Bash(" + " ".join(f(t) for t in tokens) + " *)" for f in spellings})


def merge_settings(path: Path, jobrun: str, dry: bool, fix_hooks: bool, python: str, remove=False):
    if path.exists():
        raw = path.read_text(encoding="utf-8")
        try:
            data = json.loads(raw)
        except json.JSONDecodeError as e:
            log(f"  SKIP settings.json: invalid JSON ({e}); fix it by hand, nothing changed")
            return
    else:
        data = {}
    allow = data.setdefault("permissions", {}).setdefault("allow", [])
    rules = [] if remove else limiter_rules(jobrun)
    if remove:
        data["permissions"]["allow"] = [r for r in allow if "jobrun" not in r]
    else:
        for r in rules:
            if r not in allow:
                allow.append(r)
    # hooks calling the slow python alias
    slow = []
    for event, groups in (data.get("hooks") or {}).items():
        for g in groups if isinstance(groups, list) else []:
            for h in g.get("hooks", []):
                cmd = h.get("command", "")
                if re.match(r"^\s*python(3)?\s", cmd):
                    slow.append(event)
                    if fix_hooks and not remove:
                        h["command"] = re.sub(r"^\s*python(3)?", lambda m: f"\"{python}\"", cmd, count=1)
    if slow:
        log(f"  hooks calling bare `python` (WindowsApps alias, ~+100 ms each): {sorted(set(slow))}"
            + (" -> fixed" if fix_hooks else " -> rerun with --fix-hooks to use the full path"))
    write(path, json.dumps(data, ensure_ascii=False, indent=2) + "\n", dry)


def verify(jobrun: str, python: str) -> bool:
    log("[7] verify")
    ok = True
    prefix = [p for p in re.findall(r"\"([^\"]+)\"|(\S+)", jobrun) for p in p if p]
    try:
        r = run(prefix + ["64", "1500", python, "-c", "while True: pass"], timeout=30)
        good = r.returncode == 124
        log(f"  limiter stops an infinite loop: {'OK' if good else 'FAILED rc=%s' % r.returncode}")
        ok &= good
    except (OSError, subprocess.SubprocessError) as e:
        log(f"  limiter FAILED: {e}")
        ok = False
    r = run([python, "-c", "import sys; sys.stdout.reconfigure(encoding='utf-8'); print('\\u041f\\u0440\\u0438\\u0432\\u0435\\u0442 \\u2192 ok')"])
    good = r.stdout.decode("utf-8", "replace").strip() == "\u041f\u0440\u0438\u0432\u0435\u0442 \u2192 ok"
    log(f"  python prints UTF-8: {'OK' if good else 'FAILED'}")
    return ok and good


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--home", default=os.path.expanduser("~"))
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--fix-hooks", action="store_true")
    ap.add_argument("--uninstall", action="store_true")
    ap.add_argument("--python", help="override the detected python.exe")
    a = ap.parse_args()
    if os.name != "nt":
        sys.exit("This kit targets Windows (Job Objects, CP866, WindowsApps alias).")
    sys.stdout.reconfigure(encoding="utf-8")
    claude = Path(a.home) / ".claude"
    log(f"Target: {claude}{'  (dry run)' if a.dry_run else ''}")

    if a.uninstall:
        merge_claude_md(claude / "CLAUDE.md", "", a.dry_run, remove=True)
        sk = claude / "skills" / "script-first"
        log(f"  remove {sk}")
        if not a.dry_run and sk.exists():
            shutil.rmtree(sk)
        merge_settings(claude / "settings.json", "", a.dry_run, False, "", remove=True)
        log("Done. The limiter in .claude\\tools and *.bak-* backups were left in place.")
        return

    python = a.python or find_python()
    if not python:
        sys.exit("No real python.exe found (only the WindowsApps alias or none). Install Python 3.12+ first, "
                 "or pass --python C:\\path\\to\\python.exe.")
    cp = oem_cp()
    log(f"[1] python = {python}; console OEM code page = {cp}")
    jobrun = install_limiter(claude / "tools", python, a.dry_run)
    log(f"  limiter command = {jobrun}")
    log("[3] CLAUDE.md section")
    merge_claude_md(claude / "CLAUDE.md", render("CLAUDE.section.md", python, jobrun, cp), a.dry_run)
    log("[4] skill script-first")
    write(claude / "skills" / "script-first" / "SKILL.md", render("SKILL.script-first.md", python, jobrun, cp), a.dry_run)
    log("[5-6] settings.json")
    merge_settings(claude / "settings.json", jobrun, a.dry_run, a.fix_hooks, python)
    if a.dry_run:
        log("Dry run: nothing was changed.")
        return
    ok = verify(jobrun, python)
    log("Installed. Start a NEW Claude Code session for the changes to load." if ok else
        "Installed with failed checks - see above.")
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
