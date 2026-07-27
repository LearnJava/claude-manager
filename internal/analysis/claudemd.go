package analysis

// ClaudeMdInitPrompt is the Prompt for the ephemeral "Init" session that
// GenerateClaudeMdSession (app.go) launches when a project has no CLAUDE.md.
// It replicates the built-in `/init` slash command's intent as a plain prompt
// instead of invoking `/init` itself, which only runs inside an interactive
// Claude Code session — not under `claude -p` (see CLAUDE.md's "AI-Generated
// Project Roadmap" section for the sibling pattern this mirrors: a canned
// prompt bootstrapped into a named session rather than a one-off CLI call).
const ClaudeMdInitPrompt = `Create or refine this project's CLAUDE.md - the file Claude Code auto-loads
at the start of every session here.

IF CLAUDE.md ALREADY EXISTS: read it first. Update and extend it based on what
you find in the current codebase; do not throw away accurate content, and do
not blindly overwrite sections a human may have hand-tuned.

WHAT TO GATHER
1. Read the manifest file(s) (package.json, go.mod, Cargo.toml, pyproject.toml,
   etc.), README, and top-level directory structure to identify the tech
   stack, the project's purpose, and its build/lint/test commands. Verify
   commands against actual scripts/Makefiles/CI config rather than guessing.
2. Check for existing AI-assistant rule files (.cursorrules, .cursor/rules/,
   .clinerules, .github/copilot-instructions.md, AGENTS.md) and fold anything
   relevant in, so this file doesn't contradict them.
3. Skim the source tree enough to describe the architecture: main modules/
   packages and how they depend on each other, not a file-by-file listing.

WHAT TO WRITE
- Target under ~200 lines. Markdown headers and bullets, not dense prose.
- Be specific and verifiable ("2-space indentation", not "format code nicely")
  and non-redundant - do not restate what any reader can see by opening the
  file next to the one being described.
- Cover: what the project is (1-2 sentences), tech stack, exact build/test/
  lint commands, architecture overview, and any code conventions that differ
  from the language/framework's own defaults.
- Do NOT invent gotchas, historical rationale, or business context you cannot
  verify from the code or docs in front of you - leave those for the
  project's maintainer to add later as they come up; a wrong guess here is
  worse than an absent section.

When you're done, say in one line what you created or changed, then stop -
do not start doing unrelated work in this session.`
