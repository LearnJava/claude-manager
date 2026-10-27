package experience

import (
	"sort"
	"strconv"
	"strings"

	"claude-manager/internal/store"
)

// Waste categories (WASTE-TASKS.md WS-01): useful-looking work that
// succeeded, did not repeat back-to-back (so the loop detector never fires)
// and still bought nothing.
const (
	// WasteRepeatRead is a Read/Grep/Glob that returns what an earlier call of
	// the same run already returned, with no edit in between that could have
	// changed it.
	WasteRepeatRead = "repeat_read"
	// WasteEmptySearch is a streak of two or more Grep/Glob calls with
	// different patterns that all came back (nearly) empty — guessing at names.
	WasteEmptySearch = "empty_search"
	// WasteOversizedRead is a Read whose result is far larger than the part
	// anyone needs — a whole file read where a slice would do.
	WasteOversizedRead = "oversized_read"
	// WasteRerunCmd is the same Bash command run again with no edit between
	// the runs, so it cannot print anything new.
	WasteRerunCmd = "rerun_cmd"
)

// Default thresholds. Both are overridable in [optimization]
// (waste_empty_search_chars / waste_oversized_read_tokens); 0 there means
// "use the default".
const (
	// DefaultEmptySearchChars: Claude Code answers a search without hits with
	// a one-liner ("No matches found", "No files found" — under 20 chars),
	// while the shortest real hit is a path plus a line (well over 100). 100
	// sits in the empty gap between the two.
	DefaultEmptySearchChars = 100
	// DefaultOversizedReadTokens: ≈32 000 chars, roughly an 800-line source
	// file. Past this a Read is almost always the whole file — the tool's own
	// default window is 2000 lines — while a slice with offset/limit stays
	// well below.
	DefaultOversizedReadTokens = 8000
)

// maxWasteExamples caps WasteCategory.Examples.
const maxWasteExamples = 5

// WasteThresholds are the tunable cut-offs of AnalyzeWaste. A zero field
// means the default.
type WasteThresholds struct {
	EmptySearchChars    int
	OversizedReadTokens int
}

func (t WasteThresholds) withDefaults() WasteThresholds {
	if t.EmptySearchChars <= 0 {
		t.EmptySearchChars = DefaultEmptySearchChars
	}
	if t.OversizedReadTokens <= 0 {
		t.OversizedReadTokens = DefaultOversizedReadTokens
	}
	return t
}

// WasteExample points at one wasted call.
type WasteExample struct {
	Session string
	RunID   int64 // 0 when the call has no session_runs row
	Step    int
	Tool    string
	Arg     string
	// Tokens is the wasted result's estimated size, LifetimeTokens the same
	// priced with re-reads (see attribution.go).
	Tokens         int64
	LifetimeTokens int64
}

// WasteCategory is one kind of waste over the report window.
type WasteCategory struct {
	Category       string
	Count          int
	EstTokens      int64
	LifetimeTokens int64
	// Share is LifetimeTokens as a fraction of the whole window's lifetime
	// tokens (WasteReport.TotalLifetimeTokens); 0 when that is 0.
	Share    float64
	Examples []WasteExample
}

// WasteReport is AnalyzeWaste's result. Categories lists only the kinds that
// actually occurred, most expensive first, so a clean window is an empty
// list rather than a row of zeros. One call can fall into two categories (a
// repeated read of a huge file), so the categories' counts may overlap;
// WastedCalls and WastedLifetimeTokens count each call once.
type WasteReport struct {
	TotalCalls          int
	TotalLifetimeTokens int64
	WastedCalls         int
	WastedLifetimeTokens int64
	// WastedShare is WastedLifetimeTokens / TotalLifetimeTokens.
	WastedShare float64
	Categories  []WasteCategory
}

// wasteStep is one successful call with its position in its run.
type wasteStep struct {
	row store.ActionRow
	// remaining is how many steps of the run came after it (all of them
	// re-read its result), the same weight attribution.go uses.
	remaining int
	// tokens is the estimated size of its result.
	tokens int64
}

// life prices tokens like BuildAttributionReport does: one cache write plus
// a cache read for every later step.
func (s wasteStep) life(tokens int64) float64 {
	return float64(tokens) * (cacheWriteWeight + cacheReadWeight*float64(s.remaining))
}

// wasteHit is a step flagged by a detector; tokens is the part of its result
// that was waste (the whole result, except for oversized reads).
type wasteHit struct {
	step   wasteStep
	tokens int64
}

// runKey groups rows into one conversation — the same key
// store.ActionResultChars partitions by.
func runKey(r store.ActionRow) string {
	if r.CLISessionID != "" {
		return r.CLISessionID
	}
	if r.RunID != nil {
		return strconv.FormatInt(*r.RunID, 10)
	}
	return r.Session
}

func isEditTool(tool string) bool {
	switch tool {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

// pathKey normalizes a file path argument for comparing a Read with an Edit
// of the same file (Windows paths are case-insensitive and arrive with
// either slash).
func pathKey(arg string) string {
	return strings.ToLower(strings.ReplaceAll(arg, "\\", "/"))
}

// BuildWasteReport reads project's action_signatures over the attribution
// window and runs AnalyzeWaste on them. Read only.
func BuildWasteReport(st *store.Store, project string, th WasteThresholds) (WasteReport, error) {
	rows, err := st.ActionsForWaste(project, attributionWindowDays)
	if err != nil {
		return WasteReport{}, err
	}
	return AnalyzeWaste(rows, th), nil
}

// AnalyzeWaste finds the four kinds of waste in rows (any mix of runs; the
// order does not matter). Only successful calls are judged — a failed one is
// the failure rules' business — but failed rows still keep their step
// position so the re-read weights stay right.
func AnalyzeWaste(rows []store.ActionRow, th WasteThresholds) WasteReport {
	th = th.withDefaults()

	byRun := make(map[string][]store.ActionRow)
	for _, r := range rows {
		k := runKey(r)
		byRun[k] = append(byRun[k], r)
	}

	var report WasteReport
	acc := make(map[string][]wasteHit)
	var totalLife float64
	for _, runRows := range byRun {
		sort.SliceStable(runRows, func(i, j int) bool {
			if runRows[i].StepIndex != runRows[j].StepIndex {
				return runRows[i].StepIndex < runRows[j].StepIndex
			}
			return runRows[i].ID < runRows[j].ID
		})
		last := runRows[len(runRows)-1].StepIndex
		var steps []wasteStep
		for _, r := range runRows {
			st := wasteStep{row: r, remaining: last - r.StepIndex, tokens: int64(EstimateTokens(r.ResultChars))}
			totalLife += st.life(st.tokens)
			report.TotalCalls++
			if !r.IsError {
				steps = append(steps, st)
			}
		}
		acc[WasteRepeatRead] = append(acc[WasteRepeatRead], detectRepeatRead(steps)...)
		acc[WasteEmptySearch] = append(acc[WasteEmptySearch], detectEmptySearch(steps, th.EmptySearchChars)...)
		acc[WasteOversizedRead] = append(acc[WasteOversizedRead], detectOversizedRead(steps, th.OversizedReadTokens)...)
		acc[WasteRerunCmd] = append(acc[WasteRerunCmd], detectRerunCmd(steps)...)
	}
	report.TotalLifetimeTokens = int64(totalLife)
	report.Categories = []WasteCategory{}

	// A call flagged twice is counted once in the totals, at its larger loss.
	wastedByRow := make(map[int64]float64)
	for _, cat := range []string{WasteRepeatRead, WasteEmptySearch, WasteOversizedRead, WasteRerunCmd} {
		hits := acc[cat]
		if len(hits) == 0 {
			continue
		}
		c := WasteCategory{Category: cat, Count: len(hits)}
		var life float64
		examples := make([]WasteExample, 0, len(hits))
		for _, h := range hits {
			l := h.step.life(h.tokens)
			life += l
			c.EstTokens += h.tokens
			if l > wastedByRow[h.step.row.ID] {
				wastedByRow[h.step.row.ID] = l
			}
			var runID int64
			if h.step.row.RunID != nil {
				runID = *h.step.row.RunID
			}
			examples = append(examples, WasteExample{
				Session: h.step.row.Session, RunID: runID, Step: h.step.row.StepIndex,
				Tool: h.step.row.Tool, Arg: h.step.row.Arg,
				Tokens: h.tokens, LifetimeTokens: int64(l),
			})
		}
		c.LifetimeTokens = int64(life)
		if totalLife > 0 {
			c.Share = life / totalLife
		}
		sort.SliceStable(examples, func(i, j int) bool {
			if examples[i].LifetimeTokens != examples[j].LifetimeTokens {
				return examples[i].LifetimeTokens > examples[j].LifetimeTokens
			}
			if examples[i].RunID != examples[j].RunID {
				return examples[i].RunID < examples[j].RunID
			}
			return examples[i].Step < examples[j].Step
		})
		if len(examples) > maxWasteExamples {
			examples = examples[:maxWasteExamples]
		}
		c.Examples = examples
		report.Categories = append(report.Categories, c)
	}
	sort.SliceStable(report.Categories, func(i, j int) bool {
		return report.Categories[i].LifetimeTokens > report.Categories[j].LifetimeTokens
	})

	var wasted float64
	for _, l := range wastedByRow {
		wasted += l
	}
	report.WastedCalls = len(wastedByRow)
	report.WastedLifetimeTokens = int64(wasted)
	if totalLife > 0 {
		report.WastedShare = wasted / totalLife
	}
	return report
}

// detectRepeatRead flags a Read/Grep/Glob that repeats an earlier call of the
// run with nothing in between that could change its answer: no edit of the
// same file for a Read, no edit at all for a search (its arguments do not
// record the searched path). It also has to return the same amount of text:
// the recorded Read argument is just the path, so reading a file in slices
// looks like reading it again, but the slices differ in size.
func detectRepeatRead(steps []wasteStep) []wasteHit {
	type seenKey struct {
		tool, arg string
		version   int
		chars     int
	}
	seen := make(map[seenKey]bool)
	pathVersion := make(map[string]int)
	edits := 0
	var hits []wasteHit
	for _, s := range steps {
		r := s.row
		if isEditTool(r.Tool) {
			edits++
			pathVersion[pathKey(r.Arg)]++
			continue
		}
		var version int
		switch r.Tool {
		case "Read":
			version = pathVersion[pathKey(r.Arg)]
		case "Grep", "Glob":
			version = edits
		default:
			continue
		}
		k := seenKey{tool: r.Tool, arg: r.Arg, version: version, chars: r.ResultChars}
		if seen[k] {
			hits = append(hits, wasteHit{step: s, tokens: s.tokens})
			continue
		}
		seen[k] = true
	}
	return hits
}

// detectEmptySearch flags runs of two or more adjacent Grep/Glob calls with
// at least two different patterns whose results are all shorter than
// maxChars. A single miss is a normal probe; a streak is guessing.
func detectEmptySearch(steps []wasteStep, maxChars int) []wasteHit {
	var hits []wasteHit
	var streak []wasteStep
	flush := func() {
		args := make(map[string]bool)
		for _, s := range streak {
			args[s.row.Arg] = true
		}
		if len(streak) >= 2 && len(args) >= 2 {
			for _, s := range streak {
				hits = append(hits, wasteHit{step: s, tokens: s.tokens})
			}
		}
		streak = streak[:0]
	}
	for _, s := range steps {
		if (s.row.Tool == "Grep" || s.row.Tool == "Glob") && s.row.ResultChars < maxChars {
			streak = append(streak, s)
			continue
		}
		flush()
	}
	flush()
	return hits
}

// detectOversizedRead flags a Read whose result is over maxTokens. Only the
// excess over the threshold counts as waste: that much is what a slice
// would not have carried.
func detectOversizedRead(steps []wasteStep, maxTokens int) []wasteHit {
	var hits []wasteHit
	for _, s := range steps {
		if s.row.Tool == "Read" && s.tokens > int64(maxTokens) {
			hits = append(hits, wasteHit{step: s, tokens: s.tokens - int64(maxTokens)})
		}
	}
	return hits
}

// detectRerunCmd flags a Bash command that repeats an earlier one of the run
// verbatim with no edit of any kind in between.
func detectRerunCmd(steps []wasteStep) []wasteHit {
	type seenKey struct {
		cmd     string
		version int
	}
	seen := make(map[seenKey]bool)
	edits := 0
	var hits []wasteHit
	for _, s := range steps {
		if isEditTool(s.row.Tool) {
			edits++
			continue
		}
		if s.row.Tool != "Bash" || strings.TrimSpace(s.row.Arg) == "" {
			continue
		}
		k := seenKey{cmd: s.row.Arg, version: edits}
		if seen[k] {
			hits = append(hits, wasteHit{step: s, tokens: s.tokens})
			continue
		}
		seen[k] = true
	}
	return hits
}
