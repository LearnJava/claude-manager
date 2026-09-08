package experience

import (
	"strings"
	"testing"
)

// TestSignature_Table locks in the sig produced for a range of real-world
// shaped calls: the five reference cases from LEARN-TASKS.md LN-02's own
// table, plus pipelines, chained/nav-prefixed commands, quoting, and every
// non-Bash tool's rule (path/pattern/default).
func TestSignature_Table(t *testing.T) {
	cases := []struct {
		name        string
		tool        string
		input       string
		projectPath string
		wantSig     string
	}{
		// --- LEARN-TASKS.md LN-02 reference table, verbatim ---
		{
			name:    "ref: cd chain strips nav prefix",
			tool:    "Bash",
			input:   `cd "D:/RustProjects/lumen-browser" && git status --short`,
			wantSig: "Bash:git status --short",
		},
		{
			name:    "ref: git merge masks branch and message",
			tool:    "Bash",
			input:   `git merge --no-ff p3-invest-hydration -m "…"`,
			wantSig: "Bash:git merge --no-ff <ARG> -m <ARG>",
		},
		{
			name:    "ref: cargo clippy truncates at 6 tokens",
			tool:    "Bash",
			input:   `cargo clippy -p lumen-network --all-targets -- -D warnings`,
			wantSig: "Bash:cargo clippy -p <ARG> --all-targets --",
		},
		{
			name:    "ref: bash interpreter keeps script basename",
			tool:    "Bash",
			input:   `bash scripts/worktree-pool.sh p3-work p3-bug-756 2>&1`,
			wantSig: "Bash:bash worktree-pool.sh <ARG> <ARG> <ARG>",
		},
		{
			name:    "ref: sed has no subcommand rule, masks quoted range and path",
			tool:    "Bash",
			input:   `sed -n '80,220p' scripts/scroll_perf.py`,
			wantSig: "Bash:sed -n <ARG> <ARG>",
		},

		// --- Bash: additional shapes ---
		{
			name:    "pipeline: only the first segment is signatured",
			tool:    "Bash",
			input:   `git log --oneline | head -20`,
			wantSig: "Bash:git log --oneline",
		},
		{
			name:    "chain: export prefix is skipped like cd",
			tool:    "Bash",
			input:   `export PATH=$PATH:/foo && npm run build`,
			wantSig: "Bash:npm run <ARG>",
		},
		{
			name:    "chain: semicolon-separated, first segment is nav",
			tool:    "Bash",
			input:   `cd /tmp; ls -la`,
			wantSig: "Bash:ls -la",
		},
		{
			name:    "all-nav command falls back to the only segment",
			tool:    "Bash",
			input:   `cd /tmp`,
			wantSig: "Bash:cd <ARG>",
		},
		{
			name:    "interpreter script path with a quoted space",
			tool:    "Bash",
			input:   `python "scripts/gen report.py" --fast`,
			wantSig: "Bash:python gen report.py --fast",
		},
		{
			name:    "flag value as a separate token, not =-joined",
			tool:    "Bash",
			input:   `cargo build -p sometarget --profile release`,
			wantSig: "Bash:cargo build -p <ARG> --profile <ARG>",
		},
		{
			name:    "--flag=value is truncated to --flag",
			tool:    "Bash",
			input:   `cargo build --profile=release -p sometarget`,
			wantSig: "Bash:cargo build --profile -p <ARG>",
		},
		{
			name:    "generic command with no subcommand rule keeps only flags",
			tool:    "Bash",
			input:   `rg -n "func Signature" internal/experience`,
			wantSig: "Bash:rg -n <ARG> <ARG>",
		},
		{
			name:    "windows path in an unrecognised utility is masked whole",
			tool:    "Bash",
			input:   `type D:\GolangProjects\claude-manager\CLAUDE.md`,
			wantSig: "Bash:type <ARG>",
		},

		// --- Read/Edit/Write: path relative to project, first two segments + ext ---
		{
			name:        "Read: nested path relative to project, backslashes",
			tool:        "Read",
			input:       `D:\GolangProjects\claude-manager\internal\experience\signature.go`,
			projectPath: `D:\GolangProjects\claude-manager`,
			wantSig:     "Read:internal/experience/*.go",
		},
		{
			name:    "Edit: already project-relative path",
			tool:    "Edit",
			input:   "cmd/fakeclaude/main.go",
			wantSig: "Edit:cmd/fakeclaude/*.go",
		},
		{
			name:    "Write: top-level file has no directory segments",
			tool:    "Write",
			input:   "CLAUDE.md",
			wantSig: "Write:*.md",
		},
		{
			name:        "Read: path outside the project is kept as-is",
			tool:        "Read",
			input:       `C:\Other\file.go`,
			projectPath: `D:\GolangProjects\claude-manager`,
			wantSig:     "Read:C:/Other/*.go",
		},

		// --- Grep/Glob: pattern verbatim, truncated to 40 ---
		{
			name:    "Grep: short pattern unchanged",
			tool:    "Grep",
			input:   `func \(s \*Store\)`,
			wantSig: `Grep:func \(s \*Store\)`,
		},
		{
			name:    "Glob: pattern unchanged",
			tool:    "Glob",
			input:   "**/*.svelte",
			wantSig: "Glob:**/*.svelte",
		},
		{
			name:    "Grep: pattern longer than 40 runes is truncated",
			tool:    "Grep",
			input:   strings.Repeat("a", 55),
			wantSig: "Grep:" + strings.Repeat("a", 40),
		},

		// --- default: tool name + first token, truncated to 30 ---
		{
			name:    "default: Agent description, first word",
			tool:    "Agent",
			input:   "Explore internal/experience for LN-02 groundwork",
			wantSig: "Agent:Explore",
		},
		{
			name:    "default: WebFetch URL truncated to 30 runes",
			tool:    "WebFetch",
			input:   "https://" + strings.Repeat("x", 50),
			wantSig: "WebFetch:https://" + strings.Repeat("x", 22),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSig, gotArg := Signature(tc.tool, tc.input, tc.projectPath)
			if gotSig != tc.wantSig {
				t.Errorf("Signature(%q, %q, %q) sig = %q, want %q",
					tc.tool, tc.input, tc.projectPath, gotSig, tc.wantSig)
			}
			if gotArg != tc.input {
				t.Errorf("Signature(...) arg = %q, want the input verbatim %q", gotArg, tc.input)
			}
		})
	}
}

func TestSignature_EmptyInput(t *testing.T) {
	sig, arg := Signature("Bash", "", "")
	if sig != "Bash:" {
		t.Errorf("empty Bash input: sig = %q, want %q", sig, "Bash:")
	}
	if arg != "" {
		t.Errorf("empty Bash input: arg = %q, want empty", arg)
	}
}
