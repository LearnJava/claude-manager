package session

import "strings"

// utf8Env returns env with PYTHONUTF8=1 and PYTHONIOENCODING=utf-8 added,
// keeping a value that is already set (GlobalSettings.PythonUTF8). The
// agent's Bash tool inherits the CLI's environment, so every Python it runs
// prints UTF-8 instead of the console code page — on a Russian-localized
// Windows the most common source of unreadable tool output
// (docs/Science_lab/MEASURE.md). Windows variable names are
// case-insensitive, hence EqualFold.
func utf8Env(env []string) []string {
	out := append([]string(nil), env...)
	for _, kv := range [][2]string{{"PYTHONUTF8", "1"}, {"PYTHONIOENCODING", "utf-8"}} {
		set := false
		for _, e := range out {
			if k, _, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, kv[0]) {
				set = true
				break
			}
		}
		if !set {
			out = append(out, kv[0]+"="+kv[1])
		}
	}
	return out
}
