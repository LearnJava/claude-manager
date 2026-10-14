package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"claude-manager/internal/envcheck"
)

// envProbe is what envCard needs to know about the machine; tests fake it.
type envProbe struct {
	goos   string
	look   func(string) (string, error) // exec.LookPath
	exists func(string) bool
	// installedPython finds a Windows Python that is not on PATH; nil = none.
	installedPython func() string
}

func hostProbe() envProbe {
	return envProbe{
		goos: runtime.GOOS,
		look: exec.LookPath,
		exists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		installedPython: envcheck.WindowsInstalledPython,
	}
}

// envCard renders the environment card appended to the system prompt
// (GlobalSettings.EnvCard): a few lines on the OS, shell, Python and project
// root, so the model stops guessing them. Every line answers a mistake class
// found in the saved run logs — Windows paths broken by bash quoting, tools
// Git Bash lacks, cd to a directory that is not under the current one, Read
// of a file too large for one call (docs/design-decisions.md "Command
// Guard"). Output depends only on the machine and project, so it stays
// byte-identical across runs and does not disturb prompt caching.
func envCard(p envProbe, projectPath string) string {
	var b strings.Builder
	b.WriteString("## Environment (from Claude Session Manager)\n")
	switch p.goos {
	case "windows":
		b.WriteString("- Windows. The Bash tool runs Git Bash: write Windows paths with forward slashes " +
			"(`D:/dir/file` or `/d/dir/file`). In bash a backslash right before a closing quote escapes it " +
			"(`\"D:\\dir\\\"` never closes), and unquoted backslashes are dropped.\n")
		b.WriteString("- Put scratch files in `.tmp/` inside the project, not `/tmp`: Windows programs " +
			"(Python, cargo, go) do not see Git Bash's `/tmp`.\n")
	case "darwin":
		b.WriteString("- macOS with BSD tools: `sed -i ''` (bare `sed -i` fails), no `grep -P` " +
			"(use `grep -E` or `perl -ne`), no `date -d` (use `date -j -f`).\n")
	default:
		b.WriteString("- " + osName(p.goos) + ".\n")
	}
	if missing := missingTools(p); len(missing) > 0 {
		b.WriteString("- Not installed in this shell: `" + strings.Join(missing, "`, `") + "`.\n")
	}
	b.WriteString(pythonLine(p))
	if projectPath != "" {
		b.WriteString("- Project root: `" + filepath.ToSlash(projectPath) + "`. The Bash tool keeps its working " +
			"directory between calls, so `cd` with an absolute path (or check `pwd` first).\n")
	}
	b.WriteString("- Inside double quotes a backtick runs a command: quote a pattern that contains backticks " +
		"with single quotes.\n")
	b.WriteString("- A file over ~2000 lines: `grep -n` first, then Read with numeric `offset` and `limit`.")
	return b.String()
}

func osName(goos string) string {
	switch goos {
	case "linux":
		return "Linux"
	case "freebsd", "openbsd", "netbsd":
		return "BSD (" + goos + ")"
	}
	return goos
}

// missingTools lists common text tools the model reaches for that this
// shell does not have. On Windows the Bash tool's PATH is Git Bash's, which
// the manager's own PATH usually lacks, so Git's usr/bin is checked too.
func missingTools(p envProbe) []string {
	var candidates []string
	var gitUsrBin string
	switch p.goos {
	case "windows":
		candidates = []string{"rev", "bc"}
		if git, err := p.look("git"); err == nil {
			// <Git>\cmd\git.exe or <Git>\bin\git.exe → <Git>\usr\bin
			gitUsrBin = filepath.Join(filepath.Dir(filepath.Dir(git)), "usr", "bin")
		}
	case "darwin":
		candidates = []string{"timeout"}
	default:
		return nil
	}
	var missing []string
	for _, t := range candidates {
		if _, err := p.look(t); err == nil {
			continue
		}
		if gitUsrBin != "" && p.exists(filepath.Join(gitUsrBin, t+".exe")) {
			continue
		}
		missing = append(missing, t)
	}
	return missing
}

// pythonLine names the Python command that actually works here. On Windows
// the WindowsApps python.exe is the Microsoft Store stub, not an interpreter.
func pythonLine(p envProbe) string {
	order := []string{"python3", "python"}
	if p.goos == "windows" {
		order = []string{"python", "py", "python3"}
	}
	works := func(name string) bool {
		path, err := p.look(name)
		return err == nil && !(p.goos == "windows" && strings.Contains(strings.ToLower(path), `\windowsapps\`))
	}
	for _, name := range order {
		if !works(name) {
			continue
		}
		line := "- Python: `" + name + "`"
		var absent []string
		for _, other := range []string{"python3", "python"} {
			if other != name && !works(other) {
				absent = append(absent, "`"+other+"`")
			}
		}
		if len(absent) > 0 {
			line += " (" + strings.Join(absent, ", ") + " is not available)"
		}
		return line + ".\n"
	}
	// Windows installs that are not on PATH (only the Store stub is):
	// the Python install manager and the classic per-user installer.
	if p.goos == "windows" && p.installedPython != nil {
		if exe := p.installedPython(); exe != "" {
			return "- Python: call it by full path `" + filepath.ToSlash(exe) +
				"` (`python` on PATH is the Microsoft Store stub or missing).\n"
		}
	}
	return "- Python is not on PATH.\n"
}

// selfExe is the manager binary the cmdguard hook runs (`<exe> hook
// cmdguard`); a variable so tests can pin it.
var selfExe = os.Executable
