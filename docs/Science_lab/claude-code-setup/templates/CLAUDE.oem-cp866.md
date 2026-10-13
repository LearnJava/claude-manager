
### Russian-localized Windows: native tools print CP866

Measured over 6,431 sessions: 23% of Python runs without a UTF-8 setting returned unreadable (mangled
Russian) output versus 0.9% with it; `tasklist` / `taskkill` output was unreadable in 58-67% of calls.

- Native console tools (`taskkill`, `tasklist`, `netstat`, `schtasks`, `cmd /c ...`): pipe through
  `iconv -f cp866 -t utf-8`. Prefer `tasklist //FO CSV //NH | iconv -f cp866 -t utf-8` for parsable output.
- In Python, decode their output explicitly: `subprocess.run(..., capture_output=True).stdout.decode("cp866")`.
