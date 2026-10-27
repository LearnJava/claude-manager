package suggest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// TaskFile is the file Accept writes the accepted suggestions into, as
// `## SG-NN: title` sections the queue can point at. It lives in the project
// root next to the other task files.
const TaskFile = "SUGGESTED-TASKS.md"

const taskFileHeader = "# Предложенные задачи\n\n" +
	"Задачи, найденные сканером репозитория (WS-07) и принятые человеком. Каждая добавлена в очередь\n" +
	"указателем на свой заголовок. Строка `<!-- suggest:… -->` под заголовком — ключ, по которому\n" +
	"повторный скан не предлагает то же самое; не удаляйте её, пока задача актуальна.\n"

var (
	keyRe   = regexp.MustCompile(`<!-- suggest:([a-z_]+:[0-9a-f]+) -->`)
	sgIDRe  = regexp.MustCompile(`(?m)^## SG-(\d+):`)
	queueRe = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._ /-]*$`)
)

// acceptedKeys returns the keys already written to TaskFile.
func acceptedKeys(root string) map[string]bool {
	keys := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(root, TaskFile))
	if err != nil {
		return keys
	}
	for _, m := range keyRe.FindAllStringSubmatch(string(data), -1) {
		keys[m[1]] = true
	}
	return keys
}

// ValidQueueFile reports whether name is a plain relative path inside the
// project (no drive, no `..`), the only kind Accept appends to.
func ValidQueueFile(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "..") || strings.Contains(name, `\`) {
		return false
	}
	return queueRe.MatchString(name)
}

// Accept writes the picked suggestions as sections of TaskFile and appends one
// pointer per section to queueFile (relative to root; it is created if absent).
// It returns the pointers appended, in order. Nothing is written when picked
// is empty. The caller must pass only suggestions a human ticked.
func Accept(root, queueFile string, picked []Suggestion) ([]string, error) {
	return accept(root, queueFile, picked, nil)
}

// AcceptUnlessOpen is Accept for a task a schedule files by itself (WASTE-TASKS.md
// WS-10): a suggestion whose key is already in TaskFile is skipped only while
// its section is still pointed at by one of queueFiles. Once the pointer is
// gone the task was taken and finished, so the same failure appearing again is
// a new task, not a duplicate.
func AcceptUnlessOpen(root, queueFile string, queueFiles []string, picked []Suggestion) ([]string, error) {
	open := openKeys(root, append([]string{queueFile}, queueFiles...))
	return accept(root, queueFile, picked, open)
}

// openKeys returns the keys whose latest section in TaskFile is still pointed
// at from one of queueFiles.
func openKeys(root string, queueFiles []string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(root, TaskFile))
	if err != nil {
		return out
	}
	lines := strings.Split(string(data), "\n")
	line := map[string]int{} // key -> 1-based line of its latest heading
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "## ") {
			continue
		}
		if m := keyRe.FindStringSubmatch(lines[i+1]); m != nil {
			line[m[1]] = i + 1
		}
	}
	pointers := map[string]bool{}
	for _, q := range queueFiles {
		if !ValidQueueFile(q) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(q)))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			pointers[strings.TrimSpace(l)] = true
		}
	}
	for k, n := range line {
		if pointers[fmt.Sprintf("%s:%d", TaskFile, n)] {
			out[k] = true
		}
	}
	return out
}

// accept writes picked; skip is the set of keys not to write again, nil = every
// key already in TaskFile.
func accept(root, queueFile string, picked []Suggestion, skip map[string]bool) ([]string, error) {
	if len(picked) == 0 {
		return nil, nil
	}
	if !ValidQueueFile(queueFile) {
		return nil, fmt.Errorf("suggest: %q is not a queue file inside the project", queueFile)
	}
	known := skip
	if known == nil {
		known = acceptedKeys(root)
	}
	taskPath := filepath.Join(root, TaskFile)
	existing, err := os.ReadFile(taskPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	content := string(existing)
	if content == "" {
		content = taskFileHeader
	} else if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	next := 1
	for _, m := range sgIDRe.FindAllStringSubmatch(content, -1) {
		if n, _ := strconv.Atoi(m[1]); n >= next {
			next = n + 1
		}
	}
	var pointers []string
	for _, s := range picked {
		if known[s.Key] {
			continue
		}
		known[s.Key] = true
		line := strings.Count(content, "\n") + 2 // section() opens with a blank line, then the heading
		content += section(next, s)
		pointers = append(pointers, fmt.Sprintf("%s:%d", TaskFile, line))
		next++
	}
	if len(pointers) == 0 {
		return nil, nil
	}
	if err := os.WriteFile(taskPath, []byte(content), 0o644); err != nil {
		return nil, err
	}
	qPath := filepath.Join(root, filepath.FromSlash(queueFile))
	q, err := os.ReadFile(qPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	text := string(q)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += strings.Join(pointers, "\n") + "\n"
	if err := os.WriteFile(qPath, []byte(text), 0o644); err != nil {
		return nil, err
	}
	return pointers, nil
}

func section(n int, s Suggestion) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## SG-%02d: %s\n", n, strings.ReplaceAll(s.Title, "\n", " "))
	fmt.Fprintf(&b, "<!-- suggest:%s -->\n\n", s.Key)
	b.WriteString("**Depends on:** —\n\n")
	b.WriteString("**What to do.** " + strings.TrimSpace(s.Detail) + "\n\n")
	b.WriteString("**Done when.** " + strings.TrimSpace(s.Done) + "\n")
	return b.String()
}
