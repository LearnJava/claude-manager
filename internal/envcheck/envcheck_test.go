package envcheck

import (
	"errors"
	"testing"
)

func TestNewestPythonComparesVersionsAsNumbers(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"install manager", []string{
			`C:\L\Python\pythoncore-3.9-64\python.exe`,
			`C:\L\Python\pythoncore-3.14-64\python.exe`,
			`C:\L\Python\pythoncore-3.13-64\python.exe`,
		}, `C:\L\Python\pythoncore-3.14-64\python.exe`},
		{"classic installer", []string{
			`C:\L\Programs\Python\Python39\python.exe`,
			`C:\L\Programs\Python\Python314\python.exe`,
		}, `C:\L\Programs\Python\Python314\python.exe`},
		{"across locations", []string{
			`C:\L\Python\pythoncore-3.12-64\python.exe`,
			`C:\L\Programs\Python\Python313\python.exe`,
		}, `C:\L\Programs\Python\Python313\python.exe`},
		{"regular over free-threaded", []string{
			`C:\L\Python\pythoncore-3.14t-64\python.exe`,
			`C:\L\Python\pythoncore-3.14-64\python.exe`,
		}, `C:\L\Python\pythoncore-3.14-64\python.exe`},
		{"none", nil, ""},
	}
	for _, c := range cases {
		if got := NewestPython(c.paths); got != c.want {
			t.Errorf("%s: NewestPython = %q, want %q", c.name, got, c.want)
		}
	}
}

// fakeProbe finds exactly the executables in tools (name → path) and the
// files in files.
func fakeProbe(goos string, tools map[string]string, files ...string) probe {
	return probe{
		goos: goos,
		look: func(n string) (string, error) {
			if p, ok := tools[n]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		exists: func(p string) bool {
			for _, f := range files {
				if f == p {
					return true
				}
			}
			return false
		},
		getenv:  func(string) string { return "" },
		version: func(path string, _ ...string) string { return "v-" + path },
	}
}

func byID(r Report) map[string]Tool {
	m := map[string]Tool{}
	for _, t := range r.Tools {
		m[t.ID] = t
	}
	return m
}

func TestCheckWindowsMissingTools(t *testing.T) {
	p := fakeProbe("windows", map[string]string{
		"winget": `C:\Users\k\AppData\Local\Microsoft\WindowsApps\winget.exe`,
		"git":    `C:\Program Files\Git\cmd\git.exe`,
		"python": `C:\Users\k\AppData\Local\Microsoft\WindowsApps\python.exe`, // Store stub
	}, `C:\Program Files\Git\bin\bash.exe`, `C:\Program Files\Git\usr\bin\rev.exe`)
	p.installedPython = func() string { return "" }
	p.wailsWant = "v2.16.0"

	r := check(p, "claude")
	if r.PackageManager != "winget" {
		t.Fatalf("PackageManager = %q", r.PackageManager)
	}
	tools := byID(r)

	if w := tools["winget"]; !w.Found {
		t.Error("winget lives in WindowsApps and must not be taken for the Store stub")
	}
	c := tools["claude"]
	if c.Found || c.Level != Required || c.Install != "irm https://claude.ai/install.ps1 | iex" || c.InstallVia != "powershell" {
		t.Errorf("claude = %+v", c)
	}
	if b := tools["bash"]; !b.Found || b.Path != `C:\Program Files\Git\bin\bash.exe` || b.Version == "" {
		t.Errorf("bash = %+v", b)
	}
	py := tools["python"]
	if py.Found || py.Install != "winget install --id Python.Python.3.14 -e" {
		t.Errorf("python = %+v (the WindowsApps stub is not Python)", py)
	}
	if gh := tools["gh"]; gh.Install != "winget install --id GitHub.cli -e" {
		t.Errorf("gh install = %q", gh.Install)
	}
	if w := tools["wails"]; w.Found || w.Install != "go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0" {
		t.Errorf("wails = %+v", w)
	}
	if rev := tools["rev"]; !rev.Found {
		t.Errorf("rev in Git's usr/bin must count as found: %+v", rev)
	}
	if bc := tools["bc"]; bc.Found || bc.Install != "" || bc.Note != "gitBashTool" {
		t.Errorf("bc = %+v", bc)
	}
}

func TestCheckWindowsPythonOffPath(t *testing.T) {
	p := fakeProbe("windows", map[string]string{"winget": `C:\W\winget.exe`})
	p.installedPython = func() string { return `C:\L\Python\pythoncore-3.14-64\python.exe` }
	py := byID(check(p, "claude"))["python"]
	if !py.Found || py.Install != "" || py.Note != "pythonNotOnPath" {
		t.Errorf("python = %+v", py)
	}
}

func TestCheckWailsMismatch(t *testing.T) {
	p := fakeProbe("windows", map[string]string{"wails": `C:\go\bin\wails.exe`})
	p.version = func(string, ...string) string { return "v2.12.0" }
	p.wailsWant = "v2.16.0"
	w := byID(check(p, "claude"))["wails"]
	if !w.Found || !w.Mismatch || w.Install == "" {
		t.Errorf("wails = %+v", w)
	}
}

func TestCheckLinuxPackageManagers(t *testing.T) {
	cases := map[string]string{
		"apt-get": "sudo apt install -y gh",
		"dnf":     "sudo dnf install -y gh",
		"pacman":  "sudo pacman -S --needed github-cli",
		"apk":     "sudo apk add github-cli",
	}
	for pm, want := range cases {
		r := check(fakeProbe("linux", map[string]string{pm: "/usr/bin/" + pm}), "claude")
		tools := byID(r)
		if got := tools["gh"].Install; got != want {
			t.Errorf("%s: gh install = %q, want %q", pm, got, want)
		}
		if c := tools["claude"]; c.InstallVia != "shell" {
			t.Errorf("%s: claude = %+v", pm, c)
		}
		if _, ok := tools["bash"]; ok {
			t.Errorf("%s: Git Bash is a Windows-only row", pm)
		}
	}
	r := check(fakeProbe("linux", nil), "claude")
	if r.PackageManager != "" || byID(r)["git"].Install != "" {
		t.Errorf("no package manager: %+v", byID(r)["git"])
	}
}

func TestCheckMacWithoutBrew(t *testing.T) {
	tools := byID(check(fakeProbe("darwin", nil), "claude"))
	if b := tools["brew"]; b.Found || b.InstallVia != "shell" {
		t.Errorf("brew = %+v", b)
	}
	if g := tools["git"]; g.Install != "brew install git" {
		t.Errorf("git install = %q", g.Install)
	}
}

func TestCheckCustomClaudePath(t *testing.T) {
	c := byID(check(fakeProbe("linux", nil), "/opt/claude/bin/claude"))["claude"]
	if c.Found || c.Note != "claudePath" {
		t.Errorf("claude = %+v", c)
	}
}
