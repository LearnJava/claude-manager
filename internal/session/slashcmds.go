package session

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Slash-command catalog for the message box's "/" autocomplete.
//
// What a "/" may name depends on the runtime the session drives:
//
//   - Claude Code takes `/name args` as a plain user message over stream-json
//     and expands skills, custom commands and the headless-capable built-ins
//     itself. Its system/init line lists exactly those (`slash_commands`,
//     `skills`, minus `terminal_slash_commands`), so once a run has seen init
//     that list is authoritative; before it, the catalog is rebuilt from the
//     same files Claude reads (user/project skills and commands, installed
//     plugins) plus a fixed set of built-ins.
//   - Hermes runs one `hermes chat -Q --format stream-json` process per turn,
//     and that path hands the query to the model verbatim: none of Hermes'
//     interactive slash commands (/model, /compress, …) is dispatched there.
//     Skills are what still works — `-s <skill>` preloads one for the turn —
//     so the Hermes catalog is its skill library and hermesRunTurn turns a
//     leading `/skill` into that flag (expandHermesSkills).

// SlashCommand is one entry of the "/" autocomplete list.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Kind is "skill" or "command".
	Kind string `json:"kind"`
}

const (
	SlashKindSkill   = "skill"
	SlashKindCommand = "command"
)

// claudeBuiltinCommands are Claude Code built-ins that act on a headless
// (stream-json) session. Used for descriptions, and as the built-in part of
// the catalog until the run's init line reports the real list.
var claudeBuiltinCommands = []SlashCommand{
	{Name: "compact", Description: "Compact the conversation, keeping a summary in context", Kind: SlashKindCommand},
	{Name: "clear", Description: "Clear the conversation history", Kind: SlashKindCommand},
	{Name: "context", Description: "Show current context usage", Kind: SlashKindCommand},
	{Name: "init", Description: "Initialize a CLAUDE.md file with codebase documentation", Kind: SlashKindCommand},
	{Name: "review", Description: "Review a pull request", Kind: SlashKindCommand},
	{Name: "security-review", Description: "Security review of the pending changes on the current branch", Kind: SlashKindCommand},
	{Name: "usage", Description: "Show plan usage limits", Kind: SlashKindCommand},
	{Name: "insights", Description: "Generate a report analyzing your Claude Code sessions", Kind: SlashKindCommand},
	{Name: "recap", Description: "Summarize what happened in this session", Kind: SlashKindCommand},
}

// claudeInitCommands is the slash-command part of a Claude system/init line.
type claudeInitCommands struct {
	commands []string        // slash_commands minus terminal-only ones
	skills   map[string]bool // skills
}

func newClaudeInitCommands(slash, skills, terminal []string) *claudeInitCommands {
	if len(slash) == 0 && len(skills) == 0 {
		return nil
	}
	termOnly := make(map[string]bool, len(terminal))
	for _, n := range terminal {
		termOnly[n] = true
	}
	c := &claudeInitCommands{skills: make(map[string]bool, len(skills))}
	for _, n := range skills {
		c.skills[n] = true
	}
	seen := map[string]bool{}
	for _, n := range append(append([]string{}, slash...), skills...) {
		// "__remote-workflow" and the like are internal plumbing, not for users.
		if n == "" || termOnly[n] || strings.HasPrefix(n, "__") || seen[n] {
			continue
		}
		seen[n] = true
		c.commands = append(c.commands, n)
	}
	return c
}

// ListSlashCommands returns the "/" autocomplete catalog for this session's
// runtime, sorted by name.
func (s *Session) ListSlashCommands() []SlashCommand {
	if s.Config.IsHermes() {
		return hermesSkillCommands(hermesHome(s.Config.HermesProfile))
	}
	s.mu.Lock()
	init := s.initCommands
	s.mu.Unlock()
	home, _ := os.UserHomeDir()
	return claudeSlashCommands(home, s.ProjectPath, init)
}

// claudeSlashCommands builds the Claude catalog. With init == nil it is the
// file scan plus claudeBuiltinCommands; otherwise init's names, described from
// the scan / built-ins where known.
func claudeSlashCommands(home, projectPath string, init *claudeInitCommands) []SlashCommand {
	known := map[string]SlashCommand{}
	for _, c := range claudeBuiltinCommands {
		known[c.Name] = c
	}
	// Later sources win: project over user, matching Claude's precedence.
	var scanned []SlashCommand
	if home != "" {
		scanned = append(scanned, claudePluginCommands(filepath.Join(home, ".claude", "plugins"))...)
		scanned = append(scanned, claudeDirCommands(filepath.Join(home, ".claude"))...)
	}
	if projectPath != "" {
		scanned = append(scanned, claudeDirCommands(filepath.Join(projectPath, ".claude"))...)
	}
	for _, c := range scanned {
		known[c.Name] = c
	}

	var out []SlashCommand
	if init == nil {
		for _, c := range known {
			out = append(out, c)
		}
	} else {
		for _, n := range init.commands {
			c, ok := known[n]
			if !ok {
				c = SlashCommand{Name: n, Kind: SlashKindCommand}
			}
			if init.skills[n] {
				c.Kind = SlashKindSkill
			}
			out = append(out, c)
		}
	}
	sortSlashCommands(out)
	return out
}

// claudeDirCommands reads <dir>/skills/*/SKILL.md and <dir>/commands/**/*.md
// (a sub-folder namespaces a command: commands/git/pr.md is "git:pr").
func claudeDirCommands(dir string) []SlashCommand {
	return claudeCommandsIn(dir, "")
}

// claudeCommandsIn is claudeDirCommands with an optional plugin namespace
// ("plugin:name").
func claudeCommandsIn(dir, namespace string) []SlashCommand {
	prefix := ""
	if namespace != "" {
		prefix = namespace + ":"
	}
	var out []SlashCommand
	skillsDir := filepath.Join(dir, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(skillsDir, e.Name(), "SKILL.md"))
			if err != nil {
				continue
			}
			// Claude names a skill command after its folder, not the
			// frontmatter `name` (skills/connekt with name: connekt-script-writer
			// is "/connekt" in the init line).
			_, desc := skillFrontmatter(string(data))
			out = append(out, SlashCommand{Name: prefix + e.Name(), Description: desc, Kind: SlashKindSkill})
		}
	}
	cmdDir := filepath.Join(dir, "commands")
	_ = filepath.WalkDir(cmdDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, err := filepath.Rel(cmdDir, path)
		if err != nil {
			return nil
		}
		name := strings.TrimSuffix(filepath.ToSlash(rel), filepath.Ext(rel))
		name = strings.ReplaceAll(name, "/", ":")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		_, desc := skillFrontmatter(string(data))
		if desc == "" {
			desc = firstProseLine(string(data))
		}
		out = append(out, SlashCommand{Name: prefix + name, Description: desc, Kind: SlashKindCommand})
		return nil
	})
	return out
}

// claudePluginCommands reads the skills/commands of every plugin listed in
// <pluginsDir>/installed_plugins.json, namespaced by the plugin's own name
// (.claude-plugin/plugin.json), which is what Claude prefixes them with.
func claudePluginCommands(pluginsDir string) []SlashCommand {
	data, err := os.ReadFile(filepath.Join(pluginsDir, "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &installed) != nil {
		return nil
	}
	var out []SlashCommand
	for key, installs := range installed.Plugins {
		for _, in := range installs {
			if in.InstallPath == "" {
				continue
			}
			name, _, _ := strings.Cut(key, "@")
			if raw, err := os.ReadFile(filepath.Join(in.InstallPath, ".claude-plugin", "plugin.json")); err == nil {
				var meta struct {
					Name string `json:"name"`
				}
				if json.Unmarshal(raw, &meta) == nil && meta.Name != "" {
					name = meta.Name
				}
			}
			out = append(out, claudeCommandsIn(in.InstallPath, name)...)
		}
	}
	return out
}

// hermesSkipDirs mirrors Hermes' own _SCAN_SKIP_PARTS.
var hermesSkipDirs = map[string]bool{".git": true, ".github": true, ".hub": true, ".archive": true, ".locks": true}

// hermesSkillCommands lists every SKILL.md under <home>/skills (any depth —
// Hermes groups skills into category folders), named the way Hermes slugs
// them into slash commands.
func hermesSkillCommands(home string) []SlashCommand {
	if home == "" {
		return nil
	}
	root := filepath.Join(home, "skills")
	seen := map[string]bool{}
	var out []SlashCommand
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if hermesSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		name, desc := skillFrontmatter(string(data))
		if name == "" {
			name = filepath.Base(filepath.Dir(path))
		}
		slug := hermesSkillSlug(name)
		if slug == "" || seen[slug] {
			return nil
		}
		seen[slug] = true
		if desc == "" {
			desc = firstProseLine(string(data))
		}
		out = append(out, SlashCommand{Name: slug, Description: desc, Kind: SlashKindSkill})
		return nil
	})
	sortSlashCommands(out)
	return out
}

// hermesSkillSlug mirrors Hermes' slugify_skill_name: lower-case, spaces and
// underscores to hyphens, anything else outside [a-z0-9-] dropped.
func hermesSkillSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r == ' ' || r == '_':
			b.WriteByte('-')
		case r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

// expandHermesSkills turns the leading `/skill` tokens of a Hermes prompt
// into skill names for `-s` (Hermes' -Q path would otherwise hand "/skill" to
// the model as text). Only names present in known are taken, so a prompt that
// merely starts with a path ("/usr/bin …") is left alone, and an unknown name
// never reaches `-s`, where Hermes fails the turn on it. A prompt consisting
// of skills alone gets a minimal instruction so the turn is not empty.
func expandHermesSkills(prompt string, known []SlashCommand) (string, []string) {
	if !strings.HasPrefix(prompt, "/") || len(known) == 0 {
		return prompt, nil
	}
	names := make(map[string]bool, len(known))
	for _, c := range known {
		names[c.Name] = true
	}
	const maxStacked = 5 // Hermes' own _MAX_STACKED_SKILLS
	var skills []string
	rest := prompt
	for len(skills) < maxStacked && strings.HasPrefix(rest, "/") {
		end := strings.IndexAny(rest, " \t\r\n")
		if end < 0 {
			end = len(rest)
		}
		name := rest[1:end]
		if !names[name] {
			break
		}
		skills = append(skills, name)
		rest = strings.TrimLeft(rest[end:], " \t\r\n")
	}
	if len(skills) == 0 {
		return prompt, nil
	}
	if strings.TrimSpace(rest) == "" {
		rest = "Follow the instructions of the preloaded skill: " + strings.Join(skills, ", ") + "."
	}
	return rest, skills
}

// skillFrontmatter returns the `name` and `description` of a markdown file's
// leading YAML frontmatter. Folded/literal block scalars (`>`, `|`) are
// joined into one line.
func skillFrontmatter(md string) (name, desc string) {
	md = strings.TrimPrefix(md, "\ufeff")
	if !strings.HasPrefix(md, "---") {
		return "", ""
	}
	lines := strings.Split(md, "\n")
	fields := map[string]string{}
	for i := 1; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(l) == "---" {
			break
		}
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		key, val, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "name" && key != "description" {
			continue
		}
		val = strings.TrimSpace(val)
		if val == "" || strings.HasPrefix(val, ">") || strings.HasPrefix(val, "|") {
			var parts []string
			for i+1 < len(lines) {
				next := strings.TrimRight(lines[i+1], "\r")
				if next != "" && next[0] != ' ' && next[0] != '\t' {
					break
				}
				if t := strings.TrimSpace(next); t != "" {
					parts = append(parts, t)
				}
				i++
			}
			val = strings.Join(parts, " ")
		}
		fields[key] = strings.Trim(val, `"'`)
	}
	return fields["name"], fields["description"]
}

// firstProseLine is the first non-empty, non-heading line after any
// frontmatter, capped at 120 characters.
func firstProseLine(md string) string {
	md = strings.TrimPrefix(md, "\ufeff")
	if strings.HasPrefix(md, "---") {
		if _, after, ok := strings.Cut(md[3:], "\n---"); ok {
			md = after
		}
	}
	for _, l := range strings.Split(md, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || l == "---" {
			continue
		}
		if r := []rune(l); len(r) > 120 {
			l = string(r[:120]) + "…"
		}
		return l
	}
	return ""
}

func sortSlashCommands(cs []SlashCommand) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
}
