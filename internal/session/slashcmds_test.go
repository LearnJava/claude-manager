package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"claude-manager/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(cs []SlashCommand) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func find(cs []SlashCommand, name string) (SlashCommand, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return SlashCommand{}, false
}

func TestSkillFrontmatter(t *testing.T) {
	cases := []struct {
		md, name, desc string
	}{
		{"---\nname: foo\ndescription: \"Does foo\"\n---\nbody", "foo", "Does foo"},
		{bom + "---\r\nname: bar\r\ndescription: >\r\n  line one\r\n  line two\r\n---\r\n", "bar", "line one line two"},
		{"---\ndescription: |\n  a\n  b\nname: baz\n---\n", "baz", "a b"},
		{"no frontmatter", "", ""},
	}
	for _, c := range cases {
		n, d := skillFrontmatter(c.md)
		if n != c.name || d != c.desc {
			t.Errorf("skillFrontmatter(%q) = %q, %q; want %q, %q", c.md, n, d, c.name, c.desc)
		}
	}
}

func TestFirstProseLine(t *testing.T) {
	if got := firstProseLine("---\ndescription: x\n---\n# Title\n\nDo the thing\nmore"); got != "Do the thing" {
		t.Errorf("got %q", got)
	}
	if got := firstProseLine(""); got != "" {
		t.Errorf("empty: got %q", got)
	}
}

func TestClaudeSlashCommands_ScanWithoutInit(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "skills", "user-skill", "SKILL.md"), "---\nname: pretty-name\ndescription: User skill\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "commands", "git", "pr.md"), "Open a pull request\n")
	writeFile(t, filepath.Join(proj, ".claude", "skills", "proj-skill", "SKILL.md"), "---\ndescription: Project skill\n---\n")
	writeFile(t, filepath.Join(proj, ".claude", "commands", "deploy.md"), "---\ndescription: Deploy it\n---\nbody")
	plugin := filepath.Join(home, "plugcache", "p1")
	writeFile(t, filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"nice-plugin"}`)
	writeFile(t, filepath.Join(plugin, "skills", "helper", "SKILL.md"), "---\nname: helper\ndescription: Plugin helper\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"p1@market":[{"installPath":`+jsonString(plugin)+`}]}}`)

	got := claudeSlashCommands(home, proj, nil)

	for name, want := range map[string]SlashCommand{
		"user-skill":         {Name: "user-skill", Description: "User skill", Kind: SlashKindSkill},
		"git:pr":             {Name: "git:pr", Description: "Open a pull request", Kind: SlashKindCommand},
		"proj-skill":         {Name: "proj-skill", Description: "Project skill", Kind: SlashKindSkill},
		"deploy":             {Name: "deploy", Description: "Deploy it", Kind: SlashKindCommand},
		"nice-plugin:helper": {Name: "nice-plugin:helper", Description: "Plugin helper", Kind: SlashKindSkill},
	} {
		c, ok := find(got, name)
		if !ok || c != want {
			t.Errorf("%s: got %+v (found=%v), want %+v", name, c, ok, want)
		}
	}
	if _, ok := find(got, "compact"); !ok {
		t.Error("built-in compact missing before init")
	}
	if !sortedNames(got) {
		t.Errorf("not sorted: %v", names(got))
	}
}

func TestClaudeSlashCommands_InitIsAuthoritative(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "skills", "known", "SKILL.md"), "---\nname: known\ndescription: Known skill\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "skills", "stale", "SKILL.md"), "---\nname: stale\n---\n")

	init := newClaudeInitCommands(
		[]string{"known", "compact", "doctor", "__remote-workflow", "mystery"},
		[]string{"known"},
		[]string{"doctor"},
	)
	got := claudeSlashCommands(home, "", init)

	if want := []string{"compact", "known", "mystery"}; !reflect.DeepEqual(names(got), want) {
		t.Fatalf("names = %v, want %v", names(got), want)
	}
	if c, _ := find(got, "known"); c.Kind != SlashKindSkill || c.Description != "Known skill" {
		t.Errorf("known = %+v", c)
	}
	if c, _ := find(got, "compact"); c.Kind != SlashKindCommand || c.Description == "" {
		t.Errorf("compact = %+v", c)
	}
	if c, _ := find(got, "mystery"); c.Kind != SlashKindCommand {
		t.Errorf("mystery = %+v", c)
	}
}

func TestNewClaudeInitCommands_Empty(t *testing.T) {
	if c := newClaudeInitCommands(nil, nil, []string{"x"}); c != nil {
		t.Errorf("want nil for an init line without commands, got %+v", c)
	}
}

func TestHermesSkillCommands(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "skills", "devops", "Deploy_Helper", "SKILL.md"), "---\nname: Deploy Helper\ndescription: Ship it\n---\n")
	writeFile(t, filepath.Join(home, "skills", "notes", "SKILL.md"), "---\n---\n# Notes\nTake notes\n")
	writeFile(t, filepath.Join(home, "skills", ".archive", "old", "SKILL.md"), "---\nname: old\n---\n")
	writeFile(t, filepath.Join(home, "skills", "zz-dup", "SKILL.md"), "---\nname: notes\n---\n")

	got := hermesSkillCommands(home)
	if want := []string{"deploy-helper", "notes"}; !reflect.DeepEqual(names(got), want) {
		t.Fatalf("names = %v, want %v", names(got), want)
	}
	if c, _ := find(got, "deploy-helper"); c.Description != "Ship it" || c.Kind != SlashKindSkill {
		t.Errorf("deploy-helper = %+v", c)
	}
	if c, _ := find(got, "notes"); c.Description != "Take notes" {
		t.Errorf("notes = %+v", c)
	}
	if got := hermesSkillCommands(""); got != nil {
		t.Errorf("empty home: %v", got)
	}
}

func TestHermesSkillSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Deploy Helper": "deploy-helper",
		"git_helper":    "git-helper",
		"  ok-1!  ":     "ok-1",
		"!!!":           "",
	} {
		if got := hermesSkillSlug(in); got != want {
			t.Errorf("hermesSkillSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandHermesSkills(t *testing.T) {
	known := []SlashCommand{{Name: "plan"}, {Name: "review"}}
	cases := []struct {
		in, prompt string
		skills     []string
	}{
		{"/plan add login", "add login", []string{"plan"}},
		{"/plan /review\nfix it", "fix it", []string{"plan", "review"}},
		{"/plan", "Follow the instructions of the preloaded skill: plan.", []string{"plan"}},
		{"/usr/bin is missing", "/usr/bin is missing", nil},
		{"/plan /unknown do", "/unknown do", []string{"plan"}},
		{"no slash", "no slash", nil},
	}
	for _, c := range cases {
		p, s := expandHermesSkills(c.in, known)
		if p != c.prompt || !reflect.DeepEqual(s, c.skills) {
			t.Errorf("expandHermesSkills(%q) = %q, %v; want %q, %v", c.in, p, s, c.prompt, c.skills)
		}
	}
	if p, s := expandHermesSkills("/plan x", nil); p != "/plan x" || s != nil {
		t.Errorf("no known skills: %q %v", p, s)
	}
}

func TestSession_ListSlashCommands_ByRuntime(t *testing.T) {
	hh := t.TempDir()
	t.Setenv("HERMES_HOME", hh)
	writeFile(t, filepath.Join(hh, "skills", "h-skill", "SKILL.md"), "---\nname: h-skill\n---\n")

	h := &Session{Config: config.SessionConfig{Runtime: config.RuntimeHermes}}
	if got := names(h.ListSlashCommands()); !reflect.DeepEqual(got, []string{"h-skill"}) {
		t.Errorf("hermes: %v", got)
	}

	c := &Session{ProjectPath: t.TempDir()}
	c.initCommands = newClaudeInitCommands([]string{"compact", "c-skill"}, []string{"c-skill"}, nil)
	if got := names(c.ListSlashCommands()); !reflect.DeepEqual(got, []string{"c-skill", "compact"}) {
		t.Errorf("claude: %v", got)
	}
}

func sortedNames(cs []SlashCommand) bool {
	for i := 1; i < len(cs); i++ {
		if cs[i-1].Name > cs[i].Name {
			return false
		}
	}
	return true
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// bom is a UTF-8 byte order mark, as some editors write at the top of SKILL.md.
var bom = string([]byte{0xEF, 0xBB, 0xBF})

func TestParseLine_InitSlashCommands(t *testing.T) {
	ev := ParseLine(`{"type":"system","subtype":"init","session_id":"s","slash_commands":["compact","doctor","sk"],"skills":["sk"],"terminal_slash_commands":["doctor"]}`)
	if ev.EventType != EventInit || ev.Init == nil {
		t.Fatalf("got %+v", ev)
	}
	c := newClaudeInitCommands(ev.Init.SlashCommands, ev.Init.Skills, ev.Init.TerminalSlashCommands)
	if !reflect.DeepEqual(c.commands, []string{"compact", "sk"}) || !c.skills["sk"] {
		t.Errorf("init commands = %+v", c)
	}
}
