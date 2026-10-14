package session

import (
	"path/filepath"
	"strings"
	"unicode"

	"claude-manager/internal/config"
)

// managerCard renders the Claude Manager card appended to the system prompt
// (GlobalSettings.ManagerCard): what the manager is, which sessions this
// project has and how work moves between them — task size, where specs and
// queue pointers go, how a queue session takes and closes a task. Without it
// a session reads "assign it to Developer 1" as a person in the issue
// tracker: the manager's sessions and queues exist nowhere in its context.
// The text depends only on the project's configuration, so it stays
// byte-identical across runs and keeps the prompt cache.
func managerCard(projectName, projectPath string, sessions []config.SessionConfig, self string) string {
	root := filepath.ToSlash(projectPath)
	abs := func(p string) string {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if root == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
			return p
		}
		return strings.TrimSuffix(root, "/") + "/" + p
	}

	var b strings.Builder
	b.WriteString("## Claude Manager (from Claude Session Manager)\n")
	b.WriteString("You run inside Claude Session Manager, a desktop app that runs several Claude Code sessions " +
		"per project in parallel. The names below are sessions of this app, not people: \"give/assign tasks to " +
		"Developer 1\" means put them into that session's queue as described here — never a Jira/GitHub assignee " +
		"or an issue, unless the user explicitly asks for tracker issues.\n\n")

	b.WriteString("Sessions of project `" + projectName + "`:\n")
	var own *config.SessionConfig
	for i := range sessions {
		s := &sessions[i]
		b.WriteString("- `" + s.Name + "`")
		if s.Name == self {
			b.WriteString(" (you)")
			own = s
		}
		if q := strings.TrimSpace(s.TaskSource); q != "" {
			b.WriteString(" — queue `" + abs(q) + "`")
			if s.AutoRestart {
				b.WriteString(", takes tasks one by one unattended")
			}
		} else {
			b.WriteString(" — no queue (interactive)")
		}
		if p := oneLine(s.Prompt, 120); p != "" {
			b.WriteString("; role: \"" + p + "\"")
		}
		b.WriteString("\n")
	}

	b.WriteString("\n### Giving work to a session\n")
	b.WriteString("1. Spec. Write each task as a section of a task file in the project root, one file per block " +
		"of related work: `" + abs("<BLOCK>-TASKS.md") + "` (e.g. `KM-TASKS.md`), heading `## <ID>: <title>` " +
		"(IDs like `KM-01`). The section is all the developer gets besides the repository — not this " +
		"conversation — so make it self-contained: **Depends on** (task IDs or —), **Files** (paths to touch), " +
		"**What to do** (concrete steps; name the classes, methods, tables, endpoints), **Done when** (checkable: " +
		"build and tests pass, which behaviour is visible). Write it in the user's language. If the project " +
		"already has a convention for task files (CLAUDE.md, docs), follow that instead.\n")
	b.WriteString("2. Size. One task = one run of a developer session = one branch with one concern: a few files, " +
		"roughly up to 400 changed lines, finishable in one context window. Split bigger work into dependent " +
		"tasks; fold steps smaller than a meaningful commit into their neighbour.\n")
	b.WriteString("3. Assign. A session's queue file lists one pointer per line, `<task file>:<line of the task " +
		"heading>` (e.g. `KM-TASKS.md:42`), first line = next task, nothing else on those lines (headings and " +
		"`>` notes are fine). Append pointers in execution order; do not reorder or remove pointers of tasks " +
		"that may already be in progress. Recheck the line numbers after editing the task file.\n")
	b.WriteString("4. A session with no queue cannot take tasks yet: create `" + abs("STATUS-<SESSION>.md") +
		"` (e.g. `STATUS-DEVELOPER-1.md`), fill it, and tell the user to set it as that session's Task source " +
		"in the manager (with Auto-restart to run the queue unattended). You cannot change the manager's " +
		"settings or start sessions yourself; tell the user what to start.\n")
	b.WriteString("5. Keep task and queue files in the main checkout at the paths above — developer sessions may " +
		"work in a git worktree but read them there. Commit them only if the project already tracks them or " +
		"the user asks.\n")

	if own != nil && strings.TrimSpace(own.TaskSource) != "" {
		b.WriteString("\n### Your queue\n")
		b.WriteString("Your queue is `" + abs(own.TaskSource) + "`. Take the first pointer, read the task section " +
			"it points to, implement it completely and meet its **Done when**, following the project's own git " +
			"and workflow rules (they override this card). Then delete that pointer line — the manager counts a " +
			"task done when its pointer leaves the queue (as committed on the main branch when the file is " +
			"tracked in git) and restarts you for the next one. If you are blocked, leave the pointer in place, " +
			"say why, and do not start the next task.")
	}
	return strings.TrimRight(b.String(), "\n")
}

// oneLine collapses s to a single line of at most n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
