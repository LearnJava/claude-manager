package experience

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"claude-manager/internal/store"
	"claude-manager/internal/worker"
)

// FailurePairWindow is how many steps ahead ExtractFromActionRows looks,
// within the same run, for a retry of the same signature that then succeeded
// (LEARN-TASKS.md LN-07: "в пределах 5 шагов того же прогона").
const FailurePairWindow = 5

// maxClusterExamples caps how many FixPair samples a FailureCluster keeps —
// enough for the "Failures" tab and LN-09's distillation prompt to show a
// concrete before/after, not every occurrence ever seen.
const maxClusterExamples = 5

// FixPair is one observed "it failed → the very next attempt fixed it"
// transition — the concrete evidence a FailureCluster distills into a rule
// (LEARN-TASKS.md LN-07).
type FixPair struct {
	// RunKey identifies the run this pair was observed in (a MixedTask ID for
	// source A, a run_id/cli_session_id for source B) — the unit
	// FailureCluster.DistinctRuns counts over.
	RunKey string
	// FailedArg is the command/arg that failed (source A: the failed gate's
	// own command; source B: the failing call's arg).
	FailedArg string
	// FixedArg is what worked instead (source A: the files the next round's
	// patches touched; source B: the retried call's arg) — the diff between
	// FailedArg and FixedArg is the rule.
	FixedArg string
	// Output is the failure's own raw output, when available (source A
	// only — a gate's captured stdout/stderr; source B has no output text to
	// offer, action_signatures stores only the normalized signature and
	// arg). ErrorKey is derived from this when non-empty, else from
	// FailedArg.
	Output string
}

// FailureCluster groups FixPair observations that share the same normalized
// error key (LEARN-TASKS.md LN-07's output), ranked by DistinctRuns — the
// input to LN-09's distillation prompt and the read-only "Failures" tab in
// ExperiencePanel.
type FailureCluster struct {
	ErrorKey     string
	Count        int
	DistinctRuns int
	Examples     []FixPair
}

// ExtractFromMixedTasks scans each task's round history for a "gate failed,
// next round passed" transition (LEARN-TASKS.md LN-07, source A). Gates are
// ground truth in mixed programming — never a model's own claim — so the
// failure signal is the gate's own failed command and captured output;
// what fixed it is read off the very next round's applied patches.
func ExtractFromMixedTasks(tasks []*worker.MixedTask) []FixPair {
	var pairs []FixPair
	for _, task := range tasks {
		if task == nil {
			continue
		}
		for i := 0; i+1 < len(task.Rounds); i++ {
			cur := task.Rounds[i]
			next := task.Rounds[i+1]
			if cur.Passed || !next.Passed {
				continue
			}
			failed := cur.Gates.FailedCommand()
			if failed == nil {
				continue
			}
			pairs = append(pairs, FixPair{
				RunKey:    task.ID,
				FailedArg: failed.Command,
				FixedArg:  summarizeAppliedPatches(next.Applied),
				Output:    failed.Output,
			})
		}
	}
	return pairs
}

// summarizeAppliedPatches renders the patches that turned a round green into
// a short "what fixed it" string — the files touched, since a full patch body
// is too verbose for a cluster's Examples to stay readable.
func summarizeAppliedPatches(patches []worker.Patch) string {
	seen := make(map[string]bool)
	var files []string
	for _, p := range patches {
		if p.File == "" || seen[p.File] {
			continue
		}
		seen[p.File] = true
		files = append(files, p.File)
	}
	if len(files) == 0 {
		return ""
	}
	return "patched " + strings.Join(files, ", ")
}

// ExtractFromActionRows groups rows by run (COALESCE(run_id, cli_session_id),
// mirroring TopSignatures' own fallback) and extracts fix pairs per run via
// ExtractFromRun (LEARN-TASKS.md LN-07, source B) — the entry point for a
// project-wide slice of action_signatures rows in arbitrary order.
func ExtractFromActionRows(rows []store.ActionRow) []FixPair {
	groups := make(map[string][]store.ActionRow)
	var order []string
	for _, r := range rows {
		key := actionRunKey(r)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], r)
	}

	var pairs []FixPair
	for _, key := range order {
		grp := groups[key]
		sort.Slice(grp, func(i, j int) bool { return grp[i].StepIndex < grp[j].StepIndex })
		pairs = append(pairs, ExtractFromRun(grp, key)...)
	}
	return pairs
}

// actionRunKey identifies the run one action_signatures row belongs to,
// falling back to the CLI session id for bulk-imported rows with no
// run_id — the same fallback TopSignatures' DistinctRuns already relies on.
func actionRunKey(r store.ActionRow) string {
	if r.RunID != nil {
		return fmt.Sprintf("run:%d", *r.RunID)
	}
	return "cli:" + r.CLISessionID
}

// ExtractFromRun scans one run's own rows, already ordered by StepIndex, for
// a "failed then retried, successfully" pair: a row with IsError=true
// followed within FailurePairWindow steps by a row sharing the same Sig with
// IsError=false — evidence the agent adjusted a flag or environment and it
// then worked (LEARN-TASKS.md LN-07, source B). Only the earliest such retry
// counts per failure, so one failure never yields more than one pair.
func ExtractFromRun(rows []store.ActionRow, runKey string) []FixPair {
	var pairs []FixPair
	for i, r := range rows {
		if !r.IsError {
			continue
		}
		limit := i + FailurePairWindow
		for j := i + 1; j < len(rows) && j <= limit; j++ {
			if rows[j].IsError || rows[j].Sig != r.Sig {
				continue
			}
			pairs = append(pairs, FixPair{
				RunKey:    runKey,
				FailedArg: r.Arg,
				FixedArg:  rows[j].Arg,
			})
			break
		}
	}
	return pairs
}

// errorKeySource picks the text ErrorKey normalizes: a source-A pair's own
// gate output when present, else the failing arg itself (source B has no
// output text, only the two arg strings).
func errorKeySource(p FixPair) string {
	if p.Output != "" {
		return p.Output
	}
	return p.FailedArg
}

// errorKeyMaxRunes caps the normalized error key (LEARN-TASKS.md LN-07:
// "обрезать до 80 символов") — without a cap, two occurrences of the same
// error that happen to keep diverging tails (a stack trace, say) would each
// keep their own noise and never merge.
const errorKeyMaxRunes = 80

// winPathPattern matches a Windows drive-letter absolute path anywhere in the
// text (no boundary needed — "C:" cannot appear as a mid-word substring the
// way a bare "/" can).
var winPathPattern = regexp.MustCompile(`[A-Za-z]:[\\/][^\s'"]*`)

// unixPathPattern matches a POSIX absolute path, but only when it starts at a
// word boundary (start of string, whitespace, or an opening quote/bracket) —
// otherwise "read/write" would have its "/write" half misread as a path.
var unixPathPattern = regexp.MustCompile(`(^|[\s"'(\[])(/[^\s"'()\]]+)`)

// numberPattern masks any run of digits — a line number, a token count, a
// PID — so two occurrences of the same error that differ only in such a
// count still collapse into one cluster.
var numberPattern = regexp.MustCompile(`\d+`)

// ErrorKey normalizes one line of failure text into a comparable cluster key
// (LEARN-TASKS.md LN-07): take the first line, strip absolute paths, mask
// numbers, collapse whitespace, cap length. Two occurrences of the same
// error reported from different machines/worktrees/files — different paths,
// different token counts, different line numbers — normalize to the same
// key.
func ErrorKey(text string) string {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(strings.TrimRight(line, "\r"))
	line = winPathPattern.ReplaceAllString(line, "<PATH>")
	line = unixPathPattern.ReplaceAllString(line, "${1}<PATH>")
	line = numberPattern.ReplaceAllString(line, "N")
	line = strings.Join(strings.Fields(line), " ")
	if r := []rune(line); len(r) > errorKeyMaxRunes {
		line = string(r[:errorKeyMaxRunes])
	}
	return line
}

// Cluster groups fix pairs by their normalized error key, sorted by
// DistinctRuns descending (LEARN-TASKS.md LN-07's ranking — the input to
// LN-09's distillation and the "Failures" tab). A pair whose error key
// source text is empty (no output and no failing arg — should not happen in
// practice) is dropped rather than polluting the cluster set under an empty
// key.
func Cluster(pairs []FixPair) []FailureCluster {
	type acc struct {
		count    int
		runs     map[string]bool
		examples []FixPair
	}
	groups := make(map[string]*acc)
	var order []string
	for _, p := range pairs {
		key := ErrorKey(errorKeySource(p))
		if key == "" {
			continue
		}
		g, ok := groups[key]
		if !ok {
			g = &acc{runs: make(map[string]bool)}
			groups[key] = g
			order = append(order, key)
		}
		g.count++
		if p.RunKey != "" {
			g.runs[p.RunKey] = true
		}
		if len(g.examples) < maxClusterExamples {
			g.examples = append(g.examples, p)
		}
	}

	out := make([]FailureCluster, 0, len(order))
	for _, key := range order {
		g := groups[key]
		out = append(out, FailureCluster{
			ErrorKey:     key,
			Count:        g.count,
			DistinctRuns: len(g.runs),
			Examples:     g.examples,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DistinctRuns > out[j].DistinctRuns })
	return out
}
