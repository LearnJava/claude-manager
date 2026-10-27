// Package suggest finds work a project keeps postponing and offers it as
// queue lines (WASTE-TASKS.md WS-07). Everything here is read-only and local:
// git, the project's own files and the recorded outcome of its last gate run.
// No model is called, and nothing reaches a queue except through Accept, which
// the UI calls for the entries a human ticked.
package suggest

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"claude-manager/internal/gitutil"
	"claude-manager/internal/proc"
)

// Kind is what a suggestion was found by.
type Kind string

const (
	KindTodo    Kind = "stale_todo"
	KindGates   Kind = "red_gates"
	KindDocSync Kind = "doc_drift"
	KindPointer Kind = "broken_pointer"
	KindBranch  Kind = "abandoned_branch"
	KindFolder  Kind = "abandoned_folder"

	// KindNightly marks a task filed by the scheduled gate run (WS-10). It is
	// not a scanner kind, so it is not in Kinds.
	KindNightly Kind = "nightly_gates"
)

// Kinds lists every kind in the order Scan reports them.
var Kinds = []Kind{KindTodo, KindGates, KindDocSync, KindPointer, KindBranch, KindFolder}

// Built-in thresholds (days) used when Options.StaleDays is 0.
const (
	defaultTodoDays   = 90
	defaultBranchDays = 7
	maxPerKind        = 20
	maxTodoHits       = 400
)

// Suggestion is one candidate task. Key is stable across scans (it does not
// contain a line number or a date), so a scan can tell what was already
// accepted. Detail and Done become the body of the task section.
type Suggestion struct {
	Kind   Kind
	Key    string
	Title  string
	Detail string
	Done   string
}

// GateRun is the recorded outcome of the project's latest gate run.
type GateRun struct {
	Passed  bool
	Head    string // commit the gates ran on; "" = unknown
	Summary string
	At      time.Time
}

// Options configures one scan.
type Options struct {
	Root       string   // project folder
	QueueFiles []string // task_source files of the project, relative to Root
	DocSync    []string // "code_glob => doc_path" rules
	StaleDays  int      // 0 = built-in defaults (90 for a TODO, 7 for branches/folders)
	LastGate   *GateRun // nil = no gate run recorded
	Now        time.Time
}

func (o Options) todoDays() int {
	if o.StaleDays > 0 {
		return o.StaleDays
	}
	return defaultTodoDays
}

func (o Options) branchDays() int {
	if o.StaleDays > 0 {
		return o.StaleDays
	}
	return defaultBranchDays
}

// Scan runs every detector and drops what SUGGESTED-TASKS.md already holds.
// A detector that cannot run (not a git repository, git missing) yields
// nothing instead of failing: a clean or unversioned folder is an empty list.
func Scan(ctx context.Context, o Options) ([]Suggestion, error) {
	if strings.TrimSpace(o.Root) == "" {
		return nil, fmt.Errorf("suggest: no project folder")
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	var all []Suggestion
	all = append(all, scanTodos(ctx, o)...)
	all = append(all, scanGates(ctx, o)...)
	all = append(all, scanDocSync(ctx, o)...)
	all = append(all, scanPointers(o)...)
	all = append(all, scanBranches(ctx, o)...)
	all = append(all, scanFolders(ctx, o)...)

	known := acceptedKeys(o.Root)
	out := make([]Suggestion, 0, len(all))
	seen := map[string]bool{}
	for _, s := range all {
		if known[s.Key] || seen[s.Key] {
			continue
		}
		seen[s.Key] = true
		out = append(out, s)
	}
	return out, nil
}

// NewKey builds the stable key of a finding of kind k from the parts that
// identify it (for callers outside the scanner, e.g. the nightly gate run).
func NewKey(k Kind, parts ...string) string { return makeKey(k, parts...) }

// makeKey builds the opaque stable key of a finding from the parts that
// identify it.
func makeKey(k Kind, parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return string(k) + ":" + hex.EncodeToString(h[:6])
}

// git runs git in dir and returns stdout only.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	proc.HideConsole(cmd)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// --- stale TODO / FIXME -----------------------------------------------------

type todoHit struct {
	file string
	line int
	text string
	age  time.Duration
}

// scanTodos finds TODO/FIXME lines in tracked non-markdown files whose last
// change (git blame) is older than the threshold.
func scanTodos(ctx context.Context, o Options) []Suggestion {
	out, err := git(ctx, o.Root, "grep", "-n", "-I", "-w", "-e", "TODO", "-e", "FIXME", "--",
		".", ":(exclude)*.md", ":(exclude)testdata", ":(exclude)vendor", ":(exclude)node_modules",
		":(exclude)frontend/wailsjs", ":(exclude)*.lock", ":(exclude)package-lock.json")
	if err != nil && out == "" {
		return nil
	}
	byFile := map[string][]todoHit{}
	n := 0
	for _, l := range lines(out) {
		parts := strings.SplitN(l, ":", 3)
		if len(parts) < 3 {
			continue
		}
		ln, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		if n++; n > maxTodoHits {
			break
		}
		byFile[parts[0]] = append(byFile[parts[0]], todoHit{file: parts[0], line: ln, text: strings.TrimSpace(parts[2])})
	}
	threshold := time.Duration(o.todoDays()) * 24 * time.Hour
	var stale []todoHit
	for file, hits := range byFile {
		times := blameTimes(ctx, o.Root, file)
		for _, h := range hits {
			t, ok := times[h.line]
			if !ok {
				continue
			}
			h.age = o.Now.Sub(t)
			if h.age >= threshold {
				stale = append(stale, h)
			}
		}
	}
	sort.Slice(stale, func(i, j int) bool {
		if stale[i].age != stale[j].age {
			return stale[i].age > stale[j].age
		}
		if stale[i].file != stale[j].file {
			return stale[i].file < stale[j].file
		}
		return stale[i].line < stale[j].line
	})
	if len(stale) > maxPerKind {
		stale = stale[:maxPerKind]
	}
	var res []Suggestion
	for _, h := range stale {
		text := clip(h.text, 160)
		res = append(res, Suggestion{
			Kind:  KindTodo,
			Key:   makeKey(KindTodo, h.file, h.text),
			Title: fmt.Sprintf("Разобрать TODO в %s", h.file),
			Detail: fmt.Sprintf("В `%s:%d` строка не менялась %d дн.: `%s`. Выполнить то, что в ней обещано, "+
				"или удалить её, если это уже не нужно.", h.file, h.line, int(h.age.Hours()/24), text),
			Done: "Строки с TODO/FIXME в этом месте нет: работа сделана или заметка удалена; ворота зелёные.",
		})
	}
	return res
}

// blameTimes maps a line of file to when it was last changed. Lines not yet
// committed are left out.
func blameTimes(ctx context.Context, root, file string) map[int]time.Time {
	out, err := git(ctx, root, "blame", "--porcelain", "-w", "--", file)
	if err != nil {
		return nil
	}
	hdr := regexp.MustCompile(`^([0-9a-f]{40}) \d+ (\d+)`)
	shaTime := map[string]time.Time{}
	lineSha := map[int]string{}
	cur := ""
	for _, l := range strings.Split(out, "\n") {
		if m := hdr.FindStringSubmatch(l); m != nil {
			cur = m[1]
			n, _ := strconv.Atoi(m[2])
			lineSha[n] = cur
			continue
		}
		if strings.HasPrefix(l, "author-time ") && cur != "" {
			if sec, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(l, "author-time ")), 10, 64); err == nil {
				shaTime[cur] = time.Unix(sec, 0)
			}
		}
	}
	res := map[int]time.Time{}
	for n, sha := range lineSha {
		if strings.Trim(sha, "0") == "" {
			continue // not committed yet
		}
		if t, ok := shaTime[sha]; ok {
			res[n] = t
		}
	}
	return res
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// --- red gates --------------------------------------------------------------

// scanGates: the latest recorded gate run is red and no commit has been added
// on top of the one it ran on (that commit is still the tip of a branch).
func scanGates(ctx context.Context, o Options) []Suggestion {
	g := o.LastGate
	if g == nil || g.Passed {
		return nil
	}
	if g.Head != "" {
		if _, err := git(ctx, o.Root, "cat-file", "-e", g.Head+"^{commit}"); err != nil {
			return nil // the commit is gone — nothing to point at
		}
		tips, err := git(ctx, o.Root, "for-each-ref", "--points-at="+g.Head, "--format=%(refname)", "refs/heads")
		if err != nil || len(lines(tips)) == 0 {
			return nil // something was committed on top since
		}
	}
	detail := "Последний прогон ворот закончился неудачей"
	if !g.At.IsZero() {
		detail += " (" + g.At.Format("2006-01-02 15:04") + ")"
	}
	if g.Head != "" {
		detail += " на коммите " + short(g.Head)
	}
	detail += ", и с тех пор ничего не чинилось."
	if s := strings.TrimSpace(g.Summary); s != "" {
		detail += "\n\nВывод:\n\n```\n" + clip(s, 1200) + "\n```"
	}
	return []Suggestion{{
		Kind:   KindGates,
		Key:    makeKey(KindGates, g.Head, g.Summary),
		Title:  "Починить красные ворота",
		Detail: detail,
		Done:   "Все ворота проекта проходят на текущем коммите.",
	}}
}

// --- code / doc drift -------------------------------------------------------

// ParseDocSync splits a "code_glob => doc_path" rule.
func ParseDocSync(rule string) (code, doc string, ok bool) {
	i := strings.Index(rule, "=>")
	if i < 0 {
		return "", "", false
	}
	code, doc = strings.TrimSpace(rule[:i]), strings.TrimSpace(rule[i+2:])
	return code, doc, code != "" && doc != ""
}

// scanDocSync: for each rule, the paired document's last commit is older than
// commits that changed the code it covers.
func scanDocSync(ctx context.Context, o Options) []Suggestion {
	var res []Suggestion
	for _, rule := range o.DocSync {
		code, doc, ok := ParseDocSync(rule)
		if !ok {
			continue
		}
		last, err := git(ctx, o.Root, "log", "-1", "--format=%H", "--", doc)
		docCommit := strings.TrimSpace(last)
		if err != nil || docCommit == "" {
			continue
		}
		out, err := git(ctx, o.Root, "log", "--no-merges", "--format=%s", docCommit+"..HEAD", "--", ":(glob)"+code)
		subjects := lines(out)
		if err != nil || len(subjects) == 0 {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "С последнего изменения `%s` (коммит %s) код `%s` менялся в %d коммит(ах), документ — нет.",
			doc, short(docCommit), code, len(subjects))
		b.WriteString("\n\nПоследние изменения кода:\n")
		for i, s := range subjects {
			if i == 5 {
				break
			}
			b.WriteString("- " + clip(s, 120) + "\n")
		}
		b.WriteString("\nСверить `" + doc + "` с кодом и обновить, что разошлось.")
		res = append(res, Suggestion{
			Kind:   KindDocSync,
			Key:    makeKey(KindDocSync, code, doc, docCommit),
			Title:  fmt.Sprintf("Обновить %s после изменений %s", doc, code),
			Detail: b.String(),
			Done:   "Документ описывает текущее поведение кода; расхождения, найденные при сверке, устранены.",
		})
		if len(res) >= maxPerKind {
			break
		}
	}
	return res
}

// --- queue pointers ---------------------------------------------------------

var pointerRe = regexp.MustCompile(`^(\S+):(\d+)$`)

// scanPointers: a queue line `<file>:<n>` whose line n is not a heading.
func scanPointers(o Options) []Suggestion {
	var res []Suggestion
	seen := map[string]bool{}
	for _, q := range o.QueueFiles {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.Root, filepath.FromSlash(q)))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(data), "\n") {
			s := strings.TrimSpace(l)
			if s == "" || strings.ContainsAny(s[:1], "#>-_*") {
				continue
			}
			m := pointerRe.FindStringSubmatch(s)
			if m == nil {
				continue
			}
			n, _ := strconv.Atoi(m[2])
			id := q + "\x00" + s
			if seen[id] {
				continue
			}
			seen[id] = true
			why := pointerProblem(o.Root, m[1], n)
			if why == "" {
				continue
			}
			res = append(res, Suggestion{
				Kind:  KindPointer,
				Key:   makeKey(KindPointer, q, s),
				Title: fmt.Sprintf("Исправить указатель %s в %s", s, q),
				Detail: fmt.Sprintf("Строка `%s` очереди `%s` не указывает на заголовок задачи: %s. Найти задачу в файле "+
					"и поправить номер строки, либо убрать указатель.", s, q, why),
				Done: "Каждая строка очереди указывает на заголовок задачи; панель очереди показывает задачу.",
			})
			if len(res) >= maxPerKind {
				return res
			}
		}
	}
	return res
}

func pointerProblem(root, file string, n int) string {
	if filepath.IsAbs(file) || strings.Contains(file, "..") {
		return "путь выходит за папку проекта"
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return "файла нет"
	}
	ls := strings.Split(string(data), "\n")
	if n < 1 || n > len(ls) {
		return fmt.Sprintf("в файле всего %d строк", len(ls))
	}
	if !strings.HasPrefix(strings.TrimSpace(ls[n-1]), "#") {
		return "эта строка не заголовок"
	}
	return ""
}

// --- branches and work folders ----------------------------------------------

// scanBranches: a local branch not merged into the main branch whose last
// commit is older than the threshold.
func scanBranches(ctx context.Context, o Options) []Suggestion {
	main := gitutil.MainBranch(ctx, o.Root)
	out, err := git(ctx, o.Root, "for-each-ref", "--no-merged="+main,
		"--format=%(refname:short)\t%(objectname)\t%(committerdate:unix)", "refs/heads")
	if err != nil {
		return nil
	}
	threshold := time.Duration(o.branchDays()) * 24 * time.Hour
	type br struct {
		name, sha string
		age       time.Duration
	}
	var stale []br
	for _, l := range lines(out) {
		f := strings.Split(l, "\t")
		if len(f) != 3 || f[0] == main {
			continue
		}
		sec, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			continue
		}
		if age := o.Now.Sub(time.Unix(sec, 0)); age >= threshold {
			stale = append(stale, br{f[0], f[1], age})
		}
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].age > stale[j].age })
	if len(stale) > maxPerKind {
		stale = stale[:maxPerKind]
	}
	var res []Suggestion
	for _, b := range stale {
		ahead, _ := git(ctx, o.Root, "rev-list", "--count", main+".."+b.name)
		res = append(res, Suggestion{
			Kind:  KindBranch,
			Key:   makeKey(KindBranch, b.name, b.sha),
			Title: fmt.Sprintf("Разобраться с веткой %s", b.name),
			Detail: fmt.Sprintf("Ветка `%s` не влита в `%s` (%s коммит(ов) впереди), последний коммит %d дн. назад. "+
				"Посмотреть `git log %s..%s`: довести и влить, либо удалить, если работа не нужна.",
				b.name, main, strings.TrimSpace(ahead), int(b.age.Hours()/24), main, b.name),
			Done: fmt.Sprintf("Ветка `%s` влита в `%s` или удалена.", b.name, main),
		})
	}
	return res
}

// scanFolders: a linked worktree with uncommitted changes none of which was
// touched within the threshold.
func scanFolders(ctx context.Context, o Options) []Suggestion {
	out, err := git(ctx, o.Root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	rootAbs, _ := filepath.Abs(o.Root)
	threshold := time.Duration(o.branchDays()) * 24 * time.Hour
	var paths []string
	for _, l := range lines(out) {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			paths = append(paths, p)
		}
	}
	var res []Suggestion
	for i, p := range paths {
		if i == 0 { // the main checkout
			continue
		}
		if abs, _ := filepath.Abs(p); sameDir(abs, rootAbs) {
			continue
		}
		st, err := git(ctx, p, "status", "--porcelain")
		changed := lines(st)
		if err != nil || len(changed) == 0 {
			continue
		}
		newest := newestChange(p, changed)
		if newest.IsZero() || o.Now.Sub(newest) < threshold {
			continue
		}
		branch := gitutil.CurrentBranch(ctx, p)
		if branch == "" {
			branch = "(detached HEAD)"
		}
		res = append(res, Suggestion{
			Kind:  KindFolder,
			Key:   makeKey(KindFolder, filepath.ToSlash(p), strconv.Itoa(len(changed))),
			Title: fmt.Sprintf("Разобрать незакоммиченное в %s", filepath.Base(p)),
			Detail: fmt.Sprintf("В рабочей папке `%s` (ветка %s) %d незакоммиченных изменений, последнее — %d дн. назад. "+
				"Закоммитить нужное, остальное откатить.", filepath.ToSlash(p), branch, len(changed),
				int(o.Now.Sub(newest).Hours()/24)),
			Done: "В рабочей папке нет незакоммиченных изменений.",
		})
		if len(res) >= maxPerKind {
			break
		}
	}
	return res
}

func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// newestChange is the latest mtime among the paths `git status --porcelain`
// lists (a deleted path counts as unknown and is skipped).
func newestChange(dir string, porcelain []string) time.Time {
	var newest time.Time
	for _, l := range porcelain {
		if len(l) < 4 {
			continue
		}
		p := strings.TrimSpace(l[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		p = strings.Trim(p, `"`)
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}
