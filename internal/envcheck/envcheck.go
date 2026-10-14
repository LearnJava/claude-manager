// Package envcheck finds the programs Claude Manager and its Claude sessions
// rely on and, for each missing one, the command that installs it on this
// OS with the package manager the machine has. Settings → Global shows the
// result (App.CheckEnvironment).
package envcheck

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/proc"
)

// Levels say how much a missing tool matters.
const (
	// Required: sessions do not start without it.
	Required = "required"
	// Recommended: sessions run, but hooks, skills or the model's usual
	// commands fail without it.
	Recommended = "recommended"
	// Build: only needed to rebuild the manager from source (the launcher
	// does it on every update).
	Build = "build"
	// Optional: the model is told to do without it (environment card).
	Optional = "optional"
)

// Tool is one checked program.
type Tool struct {
	ID      string // stable key; the UI looks up its description by it
	Name    string
	Level   string
	Found   bool
	Path    string
	Version string // first line of its version output; "" if not run
	// Want is the version the manager needs ("" = any); Mismatch is set
	// when the found version is a different one.
	Want     string
	Mismatch bool
	// Install is a command that installs the tool here; "" when there is
	// none (see Note). InstallVia names what runs it: winget, brew, apt,
	// dnf, pacman, zypper, apk, powershell, shell, go.
	Install    string
	InstallVia string
	// Note is an i18n key suffix for a remark the UI shows (how else to get
	// the tool, why it may be missing); "" = none.
	Note string

	versionArgs []string // how to ask it for its version; nil = do not
}

// Report is the result of one check.
type Report struct {
	OS             string // windows, darwin, linux, ...
	PackageManager string // the one install commands use; "" = none found
	Tools          []Tool
}

// probe is what Check needs from the machine; tests fake it.
type probe struct {
	goos    string
	look    func(string) (string, error)
	exists  func(string) bool
	getenv  func(string) string
	version func(path string, args ...string) string
	// installedPython finds a Windows Python that is not on PATH.
	installedPython func() string
	wailsWant       string
}

func hostProbe() probe {
	return probe{
		goos: runtime.GOOS,
		look: exec.LookPath,
		exists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		getenv:          os.Getenv,
		version:         runVersion,
		installedPython: WindowsInstalledPython,
		wailsWant:       wailsModuleVersion(),
	}
}

// Check inspects the machine. claudePath is GlobalSettings.ClaudePath.
func Check(claudePath string) Report {
	return check(hostProbe(), claudePath)
}

// spec describes how to find and install one tool.
type spec struct {
	id, name, level string
	names           []string // executables to look for, in order
	versionArgs     []string // nil = do not run it
	// pkg maps a package manager to its package name; "" key = same name
	// everywhere. A missing entry means that manager has no package.
	pkg  map[string]string
	note string
}

func check(p probe, claudePath string) Report {
	r := Report{OS: p.goos, PackageManager: packageManager(p)}

	var tools []Tool
	add := func(t Tool) { tools = append(tools, t) }

	// Package manager first: every install command below needs it.
	switch p.goos {
	case "windows":
		add(find(p, r, spec{id: "winget", name: "winget", level: Recommended, names: []string{"winget"},
			versionArgs: []string{"--version"}, note: "winget"}))
	case "darwin":
		t := find(p, r, spec{id: "brew", name: "Homebrew", level: Recommended, names: []string{"brew"},
			versionArgs: []string{"--version"}})
		if !t.Found {
			t.Install = `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`
			t.InstallVia = "shell"
		}
		add(t)
	}

	claude := find(p, r, spec{id: "claude", name: "Claude Code CLI", level: Required,
		names: []string{claudePath}, versionArgs: []string{"--version"}})
	if !claude.Found {
		if p.goos == "windows" {
			claude.Install, claude.InstallVia = "irm https://claude.ai/install.ps1 | iex", "powershell"
		} else {
			claude.Install, claude.InstallVia = "curl -fsSL https://claude.ai/install.sh | bash", "shell"
		}
		if claudePath != "claude" && claudePath != "" {
			claude.Note = "claudePath"
		}
	}
	add(claude)

	gitSpec := spec{id: "git", name: "Git", level: Required, names: []string{"git"},
		versionArgs: []string{"--version"}, pkg: map[string]string{"winget": "Git.Git", "": "git"}}
	git := find(p, r, gitSpec)
	add(git)

	if p.goos == "windows" {
		add(gitBash(p, r, git))
	}

	add(python(p, r))

	add(find(p, r, spec{id: "gh", name: "GitHub CLI", level: Recommended, names: []string{"gh"},
		versionArgs: []string{"--version"},
		pkg:         map[string]string{"winget": "GitHub.cli", "pacman": "github-cli", "apk": "github-cli", "": "gh"}}))

	add(find(p, r, spec{id: "go", name: "Go", level: Build, names: []string{"go"},
		versionArgs: []string{"version"},
		pkg:         map[string]string{"winget": "GoLang.Go", "apt": "golang-go", "dnf": "golang", "": "go"}}))
	add(find(p, r, spec{id: "node", name: "Node.js", level: Build, names: []string{"node"},
		versionArgs: []string{"--version"},
		pkg:         map[string]string{"winget": "OpenJS.NodeJS.LTS", "brew": "node", "": "nodejs"}}))
	add(wails(p, r))

	if p.goos == "windows" {
		// Git for Windows ships neither; the environment card tells the
		// model so, and there is no one-line install for Git Bash.
		for _, name := range []string{"rev", "bc"} {
			t := find(p, r, spec{id: name, name: name, level: Optional, names: []string{name}, note: "gitBashTool"})
			if !t.Found && git.Found {
				if exe := filepath.Join(gitUsrBin(git.Path), name+".exe"); p.exists(exe) {
					t.Found, t.Path, t.Note = true, exe, ""
				}
			}
			add(t)
		}
	}

	fillVersions(p, tools)
	for i := range tools {
		t := &tools[i]
		if t.Found && t.Want != "" && t.Version != "" && !strings.Contains(t.Version, t.Want) {
			t.Mismatch = true
		}
	}
	r.Tools = tools
	return r
}

// find looks the tool up and, when it is missing, fills the install command.
func find(p probe, r Report, s spec) Tool {
	t := Tool{ID: s.id, Name: s.name, Level: s.level}
	for _, n := range s.names {
		if n == "" {
			continue
		}
		if path, err := p.look(n); err == nil && !isStoreStub(p, path) {
			t.Found, t.Path = true, path
			break
		}
	}
	t.versionArgs = s.versionArgs
	if !t.Found {
		t.Install, t.InstallVia = installCommand(r.PackageManager, s.pkg)
		t.Note = s.note
	}
	return t
}

// isStoreStub reports the WindowsApps python.exe/python3.exe alias, which
// opens the Microsoft Store instead of running Python.
func isStoreStub(p probe, path string) bool {
	return p.goos == "windows" && strings.Contains(strings.ToLower(path), `\windowsapps\python`)
}

func python(p probe, r Report) Tool {
	names := []string{"python3", "python"}
	pkg := map[string]string{"winget": "Python.Python.3.14", "brew": "python", "pacman": "python", "": "python3"}
	if p.goos == "windows" {
		names = []string{"python", "py", "python3"}
	}
	t := find(p, r, spec{id: "python", name: "Python", level: Recommended, names: names,
		versionArgs: []string{"--version"}, pkg: pkg})
	if !t.Found && p.goos == "windows" && p.installedPython != nil {
		if exe := p.installedPython(); exe != "" {
			// Installed but not on PATH: no install needed, the note says
			// to call it by full path (the environment card does).
			t.Found, t.Path, t.Install, t.InstallVia, t.Note = true, exe, "", "", "pythonNotOnPath"
		}
	}
	return t
}

// gitBash finds the bash.exe Claude Code uses on Windows: the one named by
// CLAUDE_CODE_GIT_BASH_PATH, else Git for Windows' bin\bash.exe.
func gitBash(p probe, r Report, git Tool) Tool {
	t := Tool{ID: "bash", Name: "Git Bash", Level: Required, versionArgs: []string{"--version"}}
	var candidates []string
	if env := p.getenv("CLAUDE_CODE_GIT_BASH_PATH"); env != "" {
		candidates = append(candidates, env)
	}
	if git.Found {
		candidates = append(candidates, filepath.Join(gitRoot(git.Path), "bin", "bash.exe"))
	}
	for _, c := range candidates {
		if p.exists(c) {
			t.Found, t.Path = true, c
			return t
		}
	}
	t.Install, t.InstallVia = installCommand(r.PackageManager, map[string]string{"winget": "Git.Git"})
	t.Note = "gitBash"
	return t
}

func wails(p probe, r Report) Tool {
	t := find(p, r, spec{id: "wails", name: "Wails CLI", level: Build, names: []string{"wails"},
		versionArgs: []string{"version"}})
	t.Want = p.wailsWant
	want := p.wailsWant
	if want == "" {
		want = "latest"
	}
	// Also set when found, so a version mismatch is fixed by the same command.
	if !t.Found || t.Want != "" {
		t.Install, t.InstallVia = "go install github.com/wailsapp/wails/v2/cmd/wails@"+want, "go"
	}
	return t
}

// gitRoot maps <Git>\cmd\git.exe or <Git>\bin\git.exe (or
// <Git>\mingw64\bin\git.exe) to <Git>.
func gitRoot(gitPath string) string {
	dir := filepath.Dir(filepath.Dir(gitPath))
	if strings.EqualFold(filepath.Base(dir), "mingw64") {
		dir = filepath.Dir(dir)
	}
	return dir
}

func gitUsrBin(gitPath string) string { return filepath.Join(gitRoot(gitPath), "usr", "bin") }

// packageManager picks the package manager install commands use.
func packageManager(p probe) string {
	var order []string
	switch p.goos {
	case "windows":
		order = []string{"winget"}
	case "darwin":
		order = []string{"brew"}
	default:
		order = []string{"apt-get", "dnf", "pacman", "zypper", "apk", "brew"}
	}
	for _, m := range order {
		if _, err := p.look(m); err == nil {
			if m == "apt-get" {
				return "apt"
			}
			return m
		}
	}
	// Without brew on macOS the commands still say brew: the Homebrew
	// row shows how to get it first.
	if p.goos == "darwin" {
		return "brew"
	}
	if p.goos == "windows" {
		return "winget"
	}
	return ""
}

// installCommand renders the install command for a package manager.
func installCommand(pm string, pkg map[string]string) (string, string) {
	if pm == "" || pkg == nil {
		return "", ""
	}
	name, ok := pkg[pm]
	if !ok {
		if pm == "winget" {
			return "", "" // winget ids never equal the generic name
		}
		if name, ok = pkg[""]; !ok {
			return "", ""
		}
	}
	switch pm {
	case "winget":
		return "winget install --id " + name + " -e", pm
	case "brew":
		return "brew install " + name, pm
	case "apt":
		return "sudo apt install -y " + name, pm
	case "dnf":
		return "sudo dnf install -y " + name, pm
	case "pacman":
		return "sudo pacman -S --needed " + name, pm
	case "zypper":
		return "sudo zypper install -y " + name, pm
	case "apk":
		return "sudo apk add " + name, pm
	}
	return "", ""
}

// fillVersions runs the version commands of the found tools in parallel.
func fillVersions(p probe, tools []Tool) {
	var wg sync.WaitGroup
	for i := range tools {
		t := &tools[i]
		if !t.Found || t.versionArgs == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.Version = p.version(t.Path, t.versionArgs...)
		}()
	}
	wg.Wait()
}

// runVersion returns the first non-empty line of `path args...`, or "" on
// error or after 5 s.
func runVersion(path string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	proc.HideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > 100 {
				line = line[:100]
			}
			return line
		}
	}
	return ""
}

// wailsModuleVersion is the github.com/wailsapp/wails/v2 version this binary
// was built with — the Wails CLI must match it (CLAUDE.md "Known gotchas").
func wailsModuleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, d := range info.Deps {
		if d.Path == "github.com/wailsapp/wails/v2" {
			if d.Replace != nil {
				return d.Replace.Version
			}
			return d.Version
		}
	}
	return ""
}
