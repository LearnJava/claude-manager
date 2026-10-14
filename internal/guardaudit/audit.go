// Package guardaudit replays the command guard (internal/cmdguard) over saved
// Claude sessions and lists the tool failures it does not cover. It is the
// reproducible form of the study behind the guard (docs/design-decisions.md
// "Command Guard"): run it to judge a new rule before shipping it — a rule
// must never rewrite a command that worked — and to find the next kind of
// mechanical slip worth a rule. `go run ./cmd/guardaudit` is the front end.
package guardaudit

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"claude-manager/internal/cmdguard"
	"claude-manager/internal/experience"
)

// Example is one tool call kept as evidence in the report.
type Example struct {
	File    string `json:"file"`
	Cwd     string `json:"cwd,omitempty"`
	Command string `json:"command"`
	Fixed   string `json:"fixed,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RuleStat counts what one guard rule would rewrite. OnWorking is the number
// that must stay zero: each one is a command that worked and the rule would
// have changed. A call counts as failed when the tool reported an error or
// its output carries the rule's own failure text (failEvidence) — a failed
// cd followed by `;` still exits 0. Live counts commands the installed hook
// already rewrote (cmdguard.log): the transcript keeps the original text, so
// they look like working commands the rule would change.
type RuleStat struct {
	Rule      string    `json:"rule"`
	Fixed     int       `json:"fixed"`
	OnFailed  int       `json:"on_failed"`
	OnWorking int       `json:"on_working"`
	Live      int       `json:"live"`
	Examples  []Example `json:"examples"`
}

// failEvidence is the output each rule's broken command produces.
var failEvidence = map[string]*regexp.Regexp{
	cmdguard.RuleCdFromProjectRoot:       regexp.MustCompile(`cd: .*No such file or directory`),
	cmdguard.RuleQuotedTrailingBackslash: regexp.MustCompile(`unexpected EOF while looking for matching|syntax error`),
	cmdguard.RuleUnquotedWindowsPath:     regexp.MustCompile(`No such file or directory|cannot access|cannot stat|not found`),
}

// Cluster is one kind of failure the guard leaves alone, keyed by tool and
// normalized error line (experience.ErrorKey), ranked by distinct sessions.
type Cluster struct {
	Tool     string    `json:"tool"`
	Key      string    `json:"key"`
	Count    int       `json:"count"`
	Sessions int       `json:"sessions"`
	Examples []Example `json:"examples"`
}

// Report is the result of a scan.
type Report struct {
	Files     int        `json:"files"`
	ToolCalls int        `json:"tool_calls"`
	BashCalls int        `json:"bash_calls"`
	Failed    int        `json:"failed"`
	Rules     []RuleStat `json:"rules"`
	// Broken lists the rewrites of commands that worked (all rules).
	Broken    []Example `json:"broken"`
	Uncovered []Cluster `json:"uncovered"`
	// ReadErrors counts files that could not be read or parsed.
	ReadErrors int `json:"read_errors"`
	// CwdGone counts Bash calls whose recorded cwd no longer exists (a
	// removed worktree); the cd rule is not replayed for them, since its
	// answer depends on the directory tree.
	CwdGone int `json:"cwd_gone"`
}

// Options control a scan.
type Options struct {
	// Project keeps only files whose path contains this substring
	// (case-insensitive); empty keeps all.
	Project string
	// Exclude drops files whose path contains any of these substrings
	// (case-insensitive).
	Exclude []string
	// LiveFixed holds the commands the installed hook already rewrote
	// (LoadGuardLog).
	LiveFixed map[string]bool
	// Since skips files last modified before it; zero keeps all.
	Since time.Time
	// Top caps Report.Uncovered; 0 means 30.
	Top int
	// Examples caps the examples kept per rule and per cluster; 0 means 3.
	Examples int
}

// Auditor accumulates trajectories into a Report.
type Auditor struct {
	opts     Options
	rep      Report
	rules    map[string]*RuleStat
	clusters map[string]*clusterAcc
}

type clusterAcc struct {
	c        Cluster
	sessions map[string]bool
}

// NewAuditor returns an empty Auditor.
func NewAuditor(opts Options) *Auditor {
	if opts.Top <= 0 {
		opts.Top = 30
	}
	if opts.Examples <= 0 {
		opts.Examples = 3
	}
	return &Auditor{opts: opts, rules: map[string]*RuleStat{}, clusters: map[string]*clusterAcc{}}
}

// Scan walks every root for *.jsonl CLI transcripts and *.md saved run logs
// and adds each to a new Auditor. Roots that overlap (the same run as a
// transcript and as a markdown log) are counted twice.
func Scan(roots []string, opts Options) (Report, error) {
	a := NewAuditor(opts)
	project := strings.ToLower(opts.Project)
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".jsonl" && ext != ".md" {
				return nil
			}
			lower := strings.ToLower(path)
			if project != "" && !strings.Contains(lower, project) {
				return nil
			}
			for _, x := range opts.Exclude {
				if x != "" && strings.Contains(lower, strings.ToLower(x)) {
					return nil
				}
			}
			if !opts.Since.IsZero() {
				if info, err := d.Info(); err != nil || info.ModTime().Before(opts.Since) {
					return nil
				}
			}
			var traj experience.Trajectory
			if ext == ".jsonl" {
				traj, err = experience.Read(path)
			} else {
				traj, err = experience.ParseLogFile(path)
			}
			if err != nil {
				a.rep.ReadErrors++
				return nil
			}
			a.Add(path, traj)
			return nil
		})
		if err != nil {
			return a.Report(), err
		}
	}
	return a.Report(), nil
}

// Add replays the guard over one trajectory. The project root handed to the
// cd rule is the first cwd seen in the file (the directory the CLI was
// started in); the markdown backend has no cwd, which disables that rule.
// Sessions are counted per file: a markdown log names its session ("lumen/
// S1"), not the CLI run, and one file is one run on both backends.
func (a *Auditor) Add(file string, traj experience.Trajectory) {
	a.rep.Files++
	projectDir := ""
	markdown := true
	for _, st := range traj.Steps {
		if projectDir == "" && st.Cwd != "" {
			projectDir = st.Cwd
		}
		if len(st.Input) > 0 {
			markdown = false
		}
	}
	for i, st := range traj.Steps {
		if st.Kind != experience.StepToolUse || st.ToolName == "" {
			continue
		}
		a.rep.ToolCalls++
		if st.ResultIsError {
			a.rep.Failed++
		}
		fixed := false
		if st.ToolName == "Bash" {
			a.rep.BashCalls++
			fixed = a.replay(file, traj.Steps, i, projectDir, markdown)
		}
		if st.ResultIsError && !fixed {
			a.addFailure(file, st)
		}
	}
}

// markdownPairWindow is how far from a call the markdown backend may have
// put its result: it pairs results with calls first-in-first-out, so a call
// whose result was never logged, or parallel calls, shift the pairing.
const markdownPairWindow = 5

func (a *Auditor) replay(file string, steps []experience.Step, i int, projectDir string, markdown bool) bool {
	st := steps[i]
	// A markdown log keeps a command on one line, so a heredoc body reads as
	// shell words; replaying it would rewrite text that bash never parses.
	if markdown && strings.Contains(st.InputText, "<<") {
		return false
	}
	cwd := st.Cwd
	if cwd != "" && !isDir(cwd) {
		a.rep.CwdGone++
		cwd = ""
	}
	out, rules := cmdguard.FixCommand(st.InputText, cwd, projectDir)
	if len(rules) == 0 {
		return false
	}
	live := a.opts.LiveFixed[st.InputText]
	failed := st.ResultIsError || hasEvidence(rules, st.ResultText)
	if markdown && !failed {
		lo, hi := max(0, i-markdownPairWindow), min(len(steps), i+markdownPairWindow+1)
		for _, other := range steps[lo:hi] {
			if other.ResultIsError && hasEvidence(rules, other.ResultText) {
				failed = true
				break
			}
		}
	}
	ex := Example{File: file, Cwd: st.Cwd, Command: st.InputText, Fixed: out, Error: firstErrorLine(st.ResultText)}
	for _, r := range rules {
		rs := a.rules[r]
		if rs == nil {
			rs = &RuleStat{Rule: r}
			a.rules[r] = rs
		}
		rs.Fixed++
		switch {
		case live:
			rs.Live++
		case failed:
			rs.OnFailed++
		default:
			rs.OnWorking++
		}
		if len(rs.Examples) < a.opts.Examples {
			rs.Examples = append(rs.Examples, ex)
		}
	}
	if !failed && !live {
		a.rep.Broken = append(a.rep.Broken, ex)
	}
	return true
}

func hasEvidence(rules []string, text string) bool {
	for _, r := range rules {
		if re := failEvidence[r]; re != nil && re.MatchString(text) {
			return true
		}
	}
	return false
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// LoadGuardLog reads cmdguard.log (hook.go logCall: time, outcome, quoted
// command, quoted repair) and returns the commands the hook rewrote. A
// missing file is not an error: no live rewrites to account for.
func LoadGuardLog(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fixed := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 4)
		if len(parts) < 3 || parts[1] == "-" || strings.HasPrefix(parts[1], "skipped:") {
			continue
		}
		if cmd, err := strconv.Unquote(parts[2]); err == nil {
			fixed[cmd] = true
		}
	}
	return fixed, sc.Err()
}

func (a *Auditor) addFailure(file string, st experience.Step) {
	key := experience.ErrorKey(firstErrorLine(st.ResultText))
	if key == "" {
		key = "(no error text)"
	}
	id := st.ToolName + "\x00" + key
	acc := a.clusters[id]
	if acc == nil {
		acc = &clusterAcc{c: Cluster{Tool: st.ToolName, Key: key}, sessions: map[string]bool{}}
		a.clusters[id] = acc
	}
	acc.c.Count++
	acc.sessions[file] = true
	if len(acc.c.Examples) < a.opts.Examples {
		acc.c.Examples = append(acc.c.Examples, Example{File: file, Cwd: st.Cwd, Command: st.InputText, Error: firstErrorLine(st.ResultText)})
	}
}

// Report returns the accumulated report: rules by name, uncovered clusters
// by distinct sessions then count, capped at Options.Top.
func (a *Auditor) Report() Report {
	rep := a.rep
	rep.Rules = nil
	for _, rs := range a.rules {
		rep.Rules = append(rep.Rules, *rs)
	}
	sort.Slice(rep.Rules, func(i, j int) bool { return rep.Rules[i].Rule < rep.Rules[j].Rule })
	rep.Uncovered = nil
	for _, acc := range a.clusters {
		c := acc.c
		c.Sessions = len(acc.sessions)
		rep.Uncovered = append(rep.Uncovered, c)
	}
	sort.Slice(rep.Uncovered, func(i, j int) bool {
		x, y := rep.Uncovered[i], rep.Uncovered[j]
		if x.Sessions != y.Sessions {
			return x.Sessions > y.Sessions
		}
		if x.Count != y.Count {
			return x.Count > y.Count
		}
		return x.Tool+x.Key < y.Tool+y.Key
	})
	if len(rep.Uncovered) > a.opts.Top {
		rep.Uncovered = rep.Uncovered[:a.opts.Top]
	}
	return rep
}

// exitCodePrefix is the "Exit code N" a failed Bash result starts with — on
// its own line in a transcript, followed by the output on the same line in a
// markdown log.
var exitCodePrefix = regexp.MustCompile(`^Exit code -?\d+\s*`)

// firstErrorLine returns the first meaningful line of a tool's error text.
// A failed Bash call's result starts with "Exit code N", which says nothing
// about the kind of failure, so it is skipped (as are XML-ish wrapper tags
// like <tool_use_error>).
func firstErrorLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "<tool_use_error>")
		line = strings.TrimSuffix(line, "</tool_use_error>")
		line = strings.TrimSpace(line)
		line = exitCodePrefix.ReplaceAllString(line, "")
		if line == "" {
			continue
		}
		return line
	}
	return ""
}
