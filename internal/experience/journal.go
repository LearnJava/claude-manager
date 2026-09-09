package experience

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"claude-manager/internal/config"
)

// JournalFileName is the per-project episodic-memory file (LEARN-TASKS.md
// LN-06): one short section per completed task, appended to
// <project>/.claude-manager/journal.md.
const JournalFileName = "journal.md"

// journalArchiveTemplate names the monthly overflow file a rotated-out
// section moves into, keyed by the section's own date (not the date rotation
// happened to run) — so a journal.md that has been rotating for a year still
// groups a given month's history into one file.
const journalArchiveTemplate = "journal-archive-%s.md"

// MaxJournalEntries caps journal.md at this many sections; appending past the
// cap rotates the oldest sections out into a journal-archive-YYYY-MM.md file
// so the live journal never grows without bound (LEARN-TASKS.md LN-06).
const MaxJournalEntries = 50

// Entry is one distilled journal section: what got done, what was
// unexpected, and what to avoid next time — the analyst's JournalResult
// (internal/analysis/journal.go), timestamped and tied to the task pointer
// that was in progress when the run completed.
type Entry struct {
	Date      time.Time
	TaskPtr   string // e.g. "ROADMAP.md:92"; empty for a session with no task_source
	Done      string
	Surprises []string
	Avoid     []string
}

// render formats e as the markdown section AppendEntry writes and
// parseSection reads back:
//
//	## 2026-09-08 — ROADMAP.md:92
//	**Сделано:** …
//	**Неожиданно:** …
//	**Не делать:** …
func (e Entry) render() string {
	var b strings.Builder
	b.WriteString("## ")
	b.WriteString(e.Date.Format("2006-01-02"))
	if e.TaskPtr != "" {
		b.WriteString(" — ")
		b.WriteString(e.TaskPtr)
	}
	b.WriteString("\n**Сделано:** ")
	b.WriteString(oneLineOrDash(e.Done))
	b.WriteString("\n**Неожиданно:** ")
	b.WriteString(joinOrDash(e.Surprises))
	b.WriteString("\n**Не делать:** ")
	b.WriteString(joinOrDash(e.Avoid))
	return b.String()
}

func oneLineOrDash(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return "—"
	}
	return s
}

func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	return strings.Join(items, "; ")
}

// headerPattern matches a section's first line: "## 2026-09-08" or
// "## 2026-09-08 — ROADMAP.md:92".
var headerPattern = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2})(?: — (.+))?$`)

// parseSection inverts Entry.render. ok is false when s's first line is not a
// recognizable journal header (e.g. a stray line in a hand-edited file).
func parseSection(s string) (Entry, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return Entry{}, false
	}
	m := headerPattern.FindStringSubmatch(lines[0])
	if m == nil {
		return Entry{}, false
	}
	date, err := time.Parse("2006-01-02", m[1])
	if err != nil {
		return Entry{}, false
	}
	e := Entry{Date: date, TaskPtr: m[2]}
	for _, ln := range lines[1:] {
		switch {
		case strings.HasPrefix(ln, "**Сделано:** "):
			e.Done = dashToEmpty(strings.TrimPrefix(ln, "**Сделано:** "))
		case strings.HasPrefix(ln, "**Неожиданно:** "):
			e.Surprises = splitList(strings.TrimPrefix(ln, "**Неожиданно:** "))
		case strings.HasPrefix(ln, "**Не делать:** "):
			e.Avoid = splitList(strings.TrimPrefix(ln, "**Не делать:** "))
		}
	}
	return e, true
}

func dashToEmpty(s string) string {
	if s == "—" {
		return ""
	}
	return s
}

func splitList(s string) []string {
	if s == "" || s == "—" {
		return nil
	}
	parts := strings.Split(s, "; ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// readSections parses a journal-format file into its sections (each the
// exact text Entry.render produced, header line first), in file order.
// Returns (nil, nil) when the file does not exist yet.
func readSections(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: read %s: %w", path, err)
	}

	var sections []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			sections = append(sections, strings.TrimRight(strings.Join(cur, "\n"), "\n"))
			cur = nil
		}
	}
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, "## ") {
			flush()
		}
		if strings.HasPrefix(ln, "## ") || len(cur) > 0 {
			cur = append(cur, ln)
		}
	}
	flush()
	return sections, nil
}

// writeAtomic writes content (plus a trailing newline) to path via a temp
// file + rename, so a crash mid-write can never corrupt the journal
// (mirrors session.StateStore.Save's convention).
func writeAtomic(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content+"\n"), 0o644); err != nil {
		return fmt.Errorf("journal: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("journal: rename %s: %w", tmp, err)
	}
	return nil
}

// sectionMonth extracts a section's "YYYY-MM" from its own header line, or
// "unknown" if the header cannot be parsed (never expected in practice —
// readSections only ever hands back sections it recognized as headers itself,
// but this keeps AppendEntry from panicking on a hand-edited file).
func sectionMonth(section string) string {
	first, _, _ := strings.Cut(section, "\n")
	m := headerPattern.FindStringSubmatch(first)
	if m == nil {
		return "unknown"
	}
	return m[1][:7]
}

// AppendEntry appends entry as a new section to
// <projectPath>/.claude-manager/journal.md, atomically (tmp file + rename).
// Once the file holds more than MaxJournalEntries sections, the oldest
// overflow is moved verbatim into journal-archive-<YYYY-MM>.md — grouped by
// each archived section's own month — so the live journal never grows
// without bound. Unless commit is true, journal.md (and any archive file this
// call creates) is added to the project's .gitignore via
// config.EnsureGitignore: this is local run history, not something committed
// by default.
func AppendEntry(projectPath string, commit bool, entry Entry) error {
	dir := config.ProjectConfigDir(projectPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("journal: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, JournalFileName)

	sections, err := readSections(path)
	if err != nil {
		return err
	}
	sections = append(sections, entry.render())

	var archived []string
	if len(sections) > MaxJournalEntries {
		overflow := len(sections) - MaxJournalEntries
		archived = sections[:overflow]
		sections = sections[overflow:]
	}

	if err := writeAtomic(path, strings.Join(sections, "\n\n")); err != nil {
		return err
	}
	if !commit {
		if err := config.EnsureGitignore(dir, JournalFileName); err != nil {
			return err
		}
	}

	if len(archived) == 0 {
		return nil
	}
	return archiveSections(dir, commit, archived)
}

// archiveSections appends archived (already-rendered sections rotated out of
// journal.md) into their own journal-archive-<YYYY-MM>.md files, grouped by
// each section's own month, preserving file order within a group.
func archiveSections(dir string, commit bool, archived []string) error {
	groups := make(map[string][]string)
	var order []string
	for _, s := range archived {
		month := sectionMonth(s)
		if _, ok := groups[month]; !ok {
			order = append(order, month)
		}
		groups[month] = append(groups[month], s)
	}

	for _, month := range order {
		name := fmt.Sprintf(journalArchiveTemplate, month)
		path := filepath.Join(dir, name)

		existing, err := readSections(path)
		if err != nil {
			return err
		}
		all := append(existing, groups[month]...)

		if err := writeAtomic(path, strings.Join(all, "\n\n")); err != nil {
			return err
		}
		if !commit {
			if err := config.EnsureGitignore(dir, name); err != nil {
				return err
			}
		}
	}
	return nil
}

// LastEntries reads up to n of the most recent sections from
// <projectPath>/.claude-manager/journal.md, oldest of the n first (append
// order) — empty when the journal doesn't exist yet, i.e. the flag has never
// fired for this project. Any section that fails to parse (a hand-edited
// file) is skipped rather than aborting the whole read.
func LastEntries(projectPath string, n int) ([]Entry, error) {
	if n <= 0 {
		return nil, nil
	}
	path := filepath.Join(config.ProjectConfigDir(projectPath), JournalFileName)
	sections, err := readSections(path)
	if err != nil {
		return nil, err
	}
	if len(sections) > n {
		sections = sections[len(sections)-n:]
	}

	entries := make([]Entry, 0, len(sections))
	for _, s := range sections {
		if e, ok := parseSection(s); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}
