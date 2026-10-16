package session

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"claude-manager/internal/cmdguard"
	"claude-manager/internal/config"
)

func fakeProbe(goos string, onPath map[string]string, files ...string) envProbe {
	return envProbe{
		goos: goos,
		look: func(name string) (string, error) {
			if p, ok := onPath[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		exists: func(p string) bool { return slices.Contains(files, p) },
	}
}

func TestEnvCard_Windows(t *testing.T) {
	p := fakeProbe("windows", map[string]string{
		"git":    `C:\Program Files\Git\cmd\git.exe`,
		"python": `C:\Python314\python.exe`,
	}, `C:\Program Files\Git\usr\bin\bc.exe`)
	card := envCard(p, `D:\RustProjects\lumen`)
	for _, want := range []string{
		"The Bash tool runs Git Bash",
		"`.tmp/` inside the project",
		"with the Write tool and change it with Edit",
		"Not installed in this shell: `rev`.", // bc lives in Git's usr/bin
		"Python: `python` (`python3` is not available).",
		"Project root: `D:/RustProjects/lumen`.",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q:\n%s", want, card)
		}
	}
}

func TestEnvCard_WindowsStoreStubIsNotPython(t *testing.T) {
	p := fakeProbe("windows", map[string]string{
		"python": `C:\Users\k\AppData\Local\Microsoft\WindowsApps\python.exe`,
		"py":     `C:\Windows\py.exe`,
	})
	if card := envCard(p, ""); !strings.Contains(card, "Python: `py` (`python3`, `python` is not available).") {
		t.Errorf("store stub must not count as Python:\n%s", card)
	}
}

func TestEnvCard_WindowsPythonOffPath(t *testing.T) {
	p := fakeProbe("windows", map[string]string{"python": `C:\Users\k\AppData\Local\Microsoft\WindowsApps\python.exe`})
	p.installedPython = func() string { return `C:\Users\k\AppData\Local\Python\pythoncore-3.14-64\python.exe` }
	want := "Python: call it by full path `C:/Users/k/AppData/Local/Python/pythoncore-3.14-64/python.exe`"
	if card := envCard(p, ""); !strings.Contains(card, want) {
		t.Errorf("card lacks %q:\n%s", want, card)
	}
}

func TestEnvCard_MacAndLinux(t *testing.T) {
	mac := envCard(fakeProbe("darwin", map[string]string{"python3": "/usr/bin/python3"}), "/Users/k/p")
	for _, want := range []string{"BSD tools: `sed -i ''`", "Not installed in this shell: `timeout`.", "Python: `python3` (`python` is not available)."} {
		if !strings.Contains(mac, want) {
			t.Errorf("mac card lacks %q:\n%s", want, mac)
		}
	}
	linux := envCard(fakeProbe("linux", map[string]string{"python3": "/usr/bin/python3", "python": "/usr/bin/python"}), "/home/k/p")
	if !strings.Contains(linux, "- Linux.\n") || !strings.Contains(linux, "Python: `python3`.\n") || strings.Contains(linux, "Git Bash") {
		t.Errorf("linux card:\n%s", linux)
	}
}

func TestBuildCLIArgs_GuardAndCardOffKeepArgsUnchanged(t *testing.T) {
	cfg := config.SessionConfig{Model: "sonnet", SystemPromptAppend: "Be terse."}
	args := New(Params{Config: cfg}).buildCLIArgs(false)
	if hasFlag(args, "--settings") {
		t.Errorf("--settings present with command_guard off: %v", args)
	}
	if got := appendedPrompt(args); got != "Be terse." {
		t.Errorf("appended prompt changed with env_card off: %q", got)
	}
	if hasFlag(New(Params{Config: config.SessionConfig{Model: "sonnet"}}).buildCLIArgs(false), "--append-system-prompt") {
		t.Error("an empty append must stay absent with env_card off")
	}
}

func TestBuildCLIArgs_GuardAndCardOn(t *testing.T) {
	orig := selfExe
	selfExe = func() (string, error) { return `C:\apps\claude-manager.exe`, nil }
	t.Cleanup(func() { selfExe = orig })

	cfg := config.SessionConfig{Model: "sonnet", SystemPromptAppend: "Be terse."}
	args := New(Params{Config: cfg, ProjectPath: `D:\p`, CommandGuard: true, EnvCard: true}).buildCLIArgs(false)
	i := slices.Index(args, "--settings")
	if i < 0 || args[i+1] != cmdguard.SettingsJSON(`C:\apps\claude-manager.exe`) {
		t.Errorf("--settings missing or wrong: %v", args)
	}
	got := appendedPrompt(args)
	if !strings.HasPrefix(got, "Be terse.\n\n## Environment (from Claude Session Manager)\n") || !strings.Contains(got, "Project root: `D:/p`") {
		t.Errorf("card must follow the configured append:\n%s", got)
	}
}
