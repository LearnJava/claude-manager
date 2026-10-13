# claude-code-setup

Portable kit that configures Claude Code on a Windows machine for faster, cheaper and more reliable work:
Windows and encoding rules, token-cost rules, the `script-first` skill, a Job Object limiter for scripts,
an optional session handoff hook, and cost measurement scripts.

Start with **[SETUP.md](SETUP.md)** — it is written so Claude Code can follow it on its own:

> Follow `<path to this folder>\SETUP.md` and set up this machine.

Quick commands (use the full path of a real `python.exe`, not the WindowsApps alias):

```
python install.py --dry-run     # show what would change
python install.py               # install into %USERPROFILE%\.claude
python install.py --fix-hooks   # also point hooks that call bare `python` to the full path
python install.py --uninstall   # remove the kit's section, skill and permission rules
```

Tested on Windows 10 (Russian locale): install into an empty and a pre-populated `.claude`, idempotent re-run,
fallback from `jobrun.exe` to `jobrun.py`, uninstall back to the original files.
