package experience

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"claude-manager/internal/store"
)

// noiseErrPrefixes are error texts that are not lessons but user actions or
// the network: a denied prompt, an interrupt, a dropped connection.
var noiseErrPrefixes = []string{
	"The user doesn't want to proceed",
	"[Tool call interrupted",
	"Permission to use",
	"read ECONNRESET",
	"Request interrupted",
}

func isNoiseErr(text string) bool {
	for _, p := range noiseErrPrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// maxErrorExamples caps the samples kept per ErrorCluster.
const maxErrorExamples = 3

// ErrorExample is one occurrence of a recurring error: the call that failed,
// its error line, and what ran successfully afterwards.
type ErrorExample struct {
	FailedArg string `json:"failed_arg"`
	ErrText   string `json:"err_text"`
	FixedArg  string `json:"fixed_arg"`
}

// ErrorCluster groups every failed call of a project that shares one
// ErrorKey (LEARN-TASKS.md LN-37) — unlike Cluster it does not need a
// "failed then fixed" pair.
type ErrorCluster struct {
	ErrorKey     string
	Count        int
	DistinctRuns int
	LastSeen     time.Time
	Examples     []ErrorExample
}

// ErrorClusters clusters all is_error rows with a non-empty, non-noise
// ErrText by ErrorKey, sorted by DistinctRuns descending.
func ErrorClusters(rows []store.ActionRow) []ErrorCluster {
	byRun := make(map[string][]store.ActionRow)
	var runOrder []string
	for _, r := range rows {
		k := actionRunKey(r)
		if _, ok := byRun[k]; !ok {
			runOrder = append(runOrder, k)
		}
		byRun[k] = append(byRun[k], r)
	}

	type acc struct {
		c    ErrorCluster
		runs map[string]bool
	}
	groups := make(map[string]*acc)
	var order []string
	for _, rk := range runOrder {
		grp := byRun[rk]
		sort.SliceStable(grp, func(i, j int) bool { return grp[i].StepIndex < grp[j].StepIndex })
		for i, r := range grp {
			if !r.IsError || r.ErrText == "" || isNoiseErr(r.ErrText) {
				continue
			}
			key := ErrorKey(r.ErrText)
			if key == "" {
				continue
			}
			a, ok := groups[key]
			if !ok {
				a = &acc{c: ErrorCluster{ErrorKey: key}, runs: map[string]bool{}}
				groups[key] = a
				order = append(order, key)
			}
			a.c.Count++
			a.runs[rk] = true
			if r.Timestamp.After(a.c.LastSeen) {
				a.c.LastSeen = r.Timestamp
			}
			if len(a.c.Examples) < maxErrorExamples {
				a.c.Examples = append(a.c.Examples, ErrorExample{
					FailedArg: r.Arg, ErrText: r.ErrText, FixedArg: fixedArg(grp, i),
				})
			}
		}
	}

	out := make([]ErrorCluster, 0, len(order))
	for _, key := range order {
		a := groups[key]
		a.c.DistinctRuns = len(a.runs)
		out = append(out, a.c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DistinctRuns > out[j].DistinctRuns })
	return out
}

// fixedArg is the Arg of the first later error-free row within
// FailurePairWindow steps that has the same Sig, else the same Tool, else "".
func fixedArg(grp []store.ActionRow, i int) string {
	limit := i + FailurePairWindow
	for j := i + 1; j < len(grp) && j <= limit; j++ {
		if !grp[j].IsError && grp[j].Sig == grp[i].Sig {
			return grp[j].Arg
		}
	}
	for j := i + 1; j < len(grp) && j <= limit; j++ {
		if !grp[j].IsError && grp[j].Tool == grp[i].Tool {
			return grp[j].Arg
		}
	}
	return ""
}

// FailureRuleCandidates returns the project's recurring errors that reached
// MinCandidateRuns distinct runs and have no failure_rules row yet (any
// status, so a dismissed error is not proposed again).
func FailureRuleCandidates(st *store.Store, project string) ([]ErrorCluster, error) {
	rows, err := st.ActionRowsForCandidates(project, candidateWindowDays)
	if err != nil {
		return nil, err
	}
	plain := make([]store.ActionRow, len(rows))
	for i, r := range rows {
		plain[i] = r.ActionRow
	}
	existing, err := st.ListFailureRules(project)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(existing))
	for _, r := range existing {
		known[r.ErrorKey] = true
	}
	var out []ErrorCluster
	for _, c := range ErrorClusters(plain) {
		if c.DistinctRuns >= MinCandidateRuns && !known[c.ErrorKey] {
			out = append(out, c)
		}
	}
	return out, nil
}

const (
	learnedHeading = "## Learned from failures"
	// effectBeforeRuns is how many runs before the decision form the baseline.
	effectBeforeRuns = 20
	// effectWindowDays bounds the history FailureRuleEffect reads.
	effectWindowDays = 3650
)

// AppendClaudeMdRule adds "- <rule>" to the "Learned from failures" section
// of <projectPath>/CLAUDE.md, creating the file or the section as needed. An
// identical line already in the file is left alone; the file's line endings
// (CRLF or LF) are kept.
func AppendClaudeMdRule(projectPath, rule string) error {
	rule = strings.TrimSpace(strings.ReplaceAll(rule, "\r", ""))
	rule = strings.Join(strings.Split(rule, "\n"), " ")
	if rule == "" {
		return nil
	}
	path := filepath.Join(projectPath, "CLAUDE.md")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1] // trailing newline
	}

	item := "- " + rule
	head := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == learnedHeading {
			head = i
			break
		}
	}
	if head < 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, learnedHeading, "", item)
	} else {
		end := len(lines)
		for i := head + 1; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], "## ") {
				end = i
				break
			}
			if strings.TrimSpace(lines[i]) == item {
				return nil
			}
		}
		// insert after the last non-blank line of the section
		at := end
		for at > head+1 && strings.TrimSpace(lines[at-1]) == "" {
			at--
		}
		ins := []string{item}
		if at == head+1 {
			ins = []string{"", item}
		}
		lines = append(lines[:at], append(ins, lines[at:]...)...)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, nl)+nl), 0o644)
}

// FailureEffect compares how often an accepted rule's error showed up in the
// runs before and after the decision.
type FailureEffect struct {
	RunsBefore, HitsBefore int
	RunsAfter, HitsAfter   int
}

// FailureRuleEffect measures an accepted rule: the last 20 runs of the project
// that started before decided_at versus every run after it; a run is a hit
// when it holds an is_error row whose ErrorKey equals the rule's. A rule that
// is not accepted yields the zero result.
func FailureRuleEffect(st *store.Store, project string, r store.FailureRule) (FailureEffect, error) {
	var eff FailureEffect
	if r.Status != store.FailureRuleAccepted || r.DecidedAt == nil {
		return eff, nil
	}
	rows, err := st.ActionRowsForCandidates(project, effectWindowDays)
	if err != nil {
		return eff, err
	}
	type runInfo struct {
		start time.Time
		hit   bool
	}
	runs := make(map[string]*runInfo)
	for _, row := range rows {
		k := actionRunKey(row.ActionRow)
		ri, ok := runs[k]
		if !ok {
			ri = &runInfo{start: row.Timestamp}
			runs[k] = ri
		}
		if row.Timestamp.Before(ri.start) {
			ri.start = row.Timestamp
		}
		if row.IsError && row.ErrText != "" && ErrorKey(row.ErrText) == r.ErrorKey {
			ri.hit = true
		}
	}
	var before []*runInfo
	for _, ri := range runs {
		if ri.start.Before(*r.DecidedAt) {
			before = append(before, ri)
			continue
		}
		eff.RunsAfter++
		if ri.hit {
			eff.HitsAfter++
		}
	}
	sort.Slice(before, func(i, j int) bool { return before[i].start.After(before[j].start) })
	if len(before) > effectBeforeRuns {
		before = before[:effectBeforeRuns]
	}
	for _, ri := range before {
		eff.RunsBefore++
		if ri.hit {
			eff.HitsBefore++
		}
	}
	return eff, nil
}
