package analysis

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"claude-manager/internal/config"
)

//go:embed protocol/*.tmpl
var protocolTemplates embed.FS

// ProtocolParams renders the developer-session protocol for one project.
//
// Everything here is per-project because the protocol is read by the session
// itself, not by the app: a queue file it cannot name is a queue it will not
// read, and a merge into the wrong integration branch is worse than no merge.
type ProtocolParams struct {
	// QueueFile is the session's task_source, relative to the project root
	// (e.g. "STATUS-P1.md").
	QueueFile string
	// MainBranch is the integration branch task branches are cut from and
	// merged back into — "main" or "master", detected per repository.
	MainBranch string
	// Gates are the project's blocking check commands (config.ProjectConfig
	// .Gates). Empty is normal: the protocol then describes the gate in
	// language-agnostic terms instead of naming commands it cannot know.
	Gates []string
}

// protocolFile is one rendered artifact: an embedded template and where it
// lands inside the project.
type protocolFile struct {
	tmpl string // name under protocol/
	rel  string // destination path relative to the project root
	mode os.FileMode
}

var protocolFiles = []protocolFile{
	{tmpl: "git-workflow.md.tmpl", rel: filepath.Join("docs", "git-workflow.md"), mode: 0o644},
	{tmpl: "worktree-pool.sh.tmpl", rel: filepath.Join("scripts", "worktree-pool.sh"), mode: 0o755},
	{tmpl: "cm-task-start.md.tmpl", rel: filepath.Join(".claude", "skills", "cm-task-start", "SKILL.md"), mode: 0o644},
	{tmpl: "cm-task-finish.md.tmpl", rel: filepath.Join(".claude", "skills", "cm-task-finish", "SKILL.md"), mode: 0o644},
}

// WriteProtocolFiles installs the developer-session protocol into projectPath:
// docs/git-workflow.md, scripts/worktree-pool.sh and the two skills that make
// it executable (/cm-task-start, /cm-task-finish). It also gitignores the
// worktree pool directory, whose slots are local checkouts rather than sources.
//
// Existing files are never overwritten — a project may already have its own
// protocol (lumen-browser does), and its version is the authority, not this
// one. Returns the paths actually written, relative to projectPath, so the
// caller can report "installed 4 files" versus "nothing to do".
//
// This is what makes an interrupted session harmless: without a protocol
// telling it to reserve a task with a branch and merge after every commit, a
// session that dies mid-task leaves nothing the next one can find, and the task
// is quietly implemented again from scratch.
func WriteProtocolFiles(projectPath string, p ProtocolParams) ([]string, error) {
	if strings.TrimSpace(projectPath) == "" {
		return nil, fmt.Errorf("analysis: empty project path")
	}
	p.applyDefaults()

	data := p.templateData()
	var written []string
	for _, f := range protocolFiles {
		dest := filepath.Join(projectPath, f.rel)
		if _, err := os.Stat(dest); err == nil {
			continue // the project's own copy wins
		} else if !os.IsNotExist(err) {
			return written, fmt.Errorf("analysis: stat %s: %w", f.rel, err)
		}
		body, err := renderProtocolTemplate(f.tmpl, data)
		if err != nil {
			return written, err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return written, fmt.Errorf("analysis: create dir for %s: %w", f.rel, err)
		}
		if err := os.WriteFile(dest, []byte(body), f.mode); err != nil {
			return written, fmt.Errorf("analysis: write %s: %w", f.rel, err)
		}
		written = append(written, filepath.ToSlash(f.rel))
	}

	// Pool slots are working copies of this same repository; committing them
	// would be committing the repo into itself.
	if err := config.EnsureGitignore(projectPath, ".claude/worktrees/"); err != nil {
		return written, err
	}
	return written, nil
}

// applyDefaults fills the fields a caller may legitimately not know.
func (p *ProtocolParams) applyDefaults() {
	if strings.TrimSpace(p.QueueFile) == "" {
		p.QueueFile = statusFileName
	}
	if strings.TrimSpace(p.MainBranch) == "" {
		p.MainBranch = "main"
	}
}

// templateData expands ProtocolParams into what the templates reference,
// including the two gate renderings: a shell snippet for the workflow doc and
// prose-plus-commands for the finish skill.
func (p ProtocolParams) templateData() map[string]string {
	return map[string]string{
		"QueueFile":        p.QueueFile,
		"MainBranch":       p.MainBranch,
		"GateBlock":        p.gateBlock(),
		"GateInstructions": p.gateInstructions(),
	}
}

// gateBlock renders the pre-commit check as shell lines for the workflow doc.
func (p ProtocolParams) gateBlock() string {
	if len(p.Gates) == 0 {
		return "<the project's own build/lint/test commands>   # local gate"
	}
	lines := make([]string, 0, len(p.Gates))
	for _, g := range p.Gates {
		if g = strings.TrimSpace(g); g != "" {
			lines = append(lines, g)
		}
	}
	if len(lines) == 0 {
		return "<the project's own build/lint/test commands>   # local gate"
	}
	return strings.Join(lines, " && ") + "   # local gate"
}

// gateInstructions renders step 1 of the finish skill. With configured gates it
// is a runnable command list; without them it describes what a gate must be,
// deliberately naming no build system — this app writes protocols into projects
// whose language it does not know.
func (p ProtocolParams) gateInstructions() string {
	var b strings.Builder
	if len(p.Gates) > 0 {
		b.WriteString("This project's configured gates, in order, all of them green:\n\n```bash\n")
		for _, g := range p.Gates {
			if g = strings.TrimSpace(g); g != "" {
				b.WriteString(g)
				b.WriteString("\n")
			}
		}
		b.WriteString("```\n\nWrite the output to a file and grep the file; do not re-run a gate to")
		b.WriteString(" filter it differently.")
		return b.String()
	}
	b.WriteString("Run this project's own checks, project-wide, at maximum strictness:\n\n")
	b.WriteString("1. The strictest static-analysis/lint pass the project has, over the whole\n")
	b.WriteString("   project, warnings treated as errors where the tool supports it. Every\n")
	b.WriteString("   finding is fixed; a suppression needs a stated reason.\n")
	b.WriteString("2. The tests covering what you touched — the full suite if that is cheap\n")
	b.WriteString("   here. Never finish on failing tests.\n\n")
	b.WriteString("Write the output to a file and grep the file; do not re-run a check to")
	b.WriteString(" filter it differently.")
	return b.String()
}

// renderProtocolTemplate expands one embedded template. Missing keys are an
// error rather than an empty string: a protocol that silently says "merge into
// " is worse than one that fails to install.
func renderProtocolTemplate(name string, data map[string]string) (string, error) {
	raw, err := protocolTemplates.ReadFile("protocol/" + name)
	if err != nil {
		return "", fmt.Errorf("analysis: read template %s: %w", name, err)
	}
	t, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("analysis: parse template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("analysis: render template %s: %w", name, err)
	}
	return buf.String(), nil
}
