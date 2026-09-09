package experience

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
)

// RegressionWindow is how many of a session's most recent finished runs
// (excluding the one just finished) form the baseline a new run is compared
// against (LEARN-TASKS.md LN-16: "скользящая медиана input_tokens и
// total_cost_usd по последним 10 прогонам той же сессии").
const RegressionWindow = 10

// MinRegressionHistory is the smallest baseline DetectRegression will act
// on. Fewer prior runs than this and a "median" would be noise, not a
// trend — LEARN-TASKS.md LN-16's own "мало данных" case, where no
// regression is ever reported rather than firing on a thin sample.
const MinRegressionHistory = 3

// RegressionFactor is how many times the baseline median a new run's cost or
// input tokens must reach to count as a regression (LEARN-TASKS.md LN-16:
// "дороже 2× медианы").
const RegressionFactor = 2.0

// journalTailEntries is how many of the project journal's most recent
// sections count toward the prompt-overhead hint below — the "tail"
// primer.go's own deferred section 6 was meant to read (see BuildPrimer's
// doc comment), not the whole file.
const journalTailEntries = 3

// RegressionResult is DetectRegression's positive outcome; nil, nil means no
// regression.
type RegressionResult struct {
	// Factor is the larger of the cost/input-token ratios that crossed
	// RegressionFactor, e.g. 2.4 for a run 2.4x its session's own median.
	Factor float64
	// Hint is a best-effort explanation of what grew in the manager's own
	// prompt overhead since the last time it was measured for this session
	// (primer, skill descriptions, journal tail) — "" when nothing grew, or
	// when this is the first measurement, or when tracker is nil.
	Hint string
}

// DetectRegression compares a just-finished run's cost and input tokens
// against the trailing median of its own session's recent history
// (LEARN-TASKS.md LN-16). runID must already be persisted — finishRun calls
// store.UpdateRun before calling this, so the row DetectRegression reads
// back for runID carries the run's final numbers. Returns nil, nil when
// there isn't yet enough history (MinRegressionHistory) or neither metric
// reached RegressionFactor times the median — the "шум"/"мало данных" test
// cases.
//
// tracker may be nil (e.g. a test that only cares about detection): the
// regression is still detected, Hint just stays empty.
func DetectRegression(st *store.Store, tracker *OverheadTracker, project, sessionName, projectPath, taskDesc string, gates []string, runID int64) (*RegressionResult, error) {
	runs, err := st.ListRuns(project, sessionName, 0)
	if err != nil {
		return nil, err
	}

	var current *store.SessionRun
	history := make([]*store.SessionRun, 0, RegressionWindow)
	for _, r := range runs {
		if r.ID == runID {
			current = r
			continue
		}
		if current == nil {
			// runs is newest-first; a row seen before the current run's own
			// row is newer than it and cannot be history.
			continue
		}
		history = append(history, r)
		if len(history) >= RegressionWindow {
			break
		}
	}
	if current == nil || len(history) < MinRegressionHistory {
		return nil, nil
	}

	costs := make([]float64, len(history))
	inputs := make([]float64, len(history))
	for i, r := range history {
		costs[i] = r.TotalCostUSD
		inputs[i] = float64(r.InputTokens)
	}
	sort.Float64s(costs)
	sort.Float64s(inputs)
	medianCost := percentile(costs, 0.5)
	medianInput := percentile(inputs, 0.5)

	factor := 0.0
	if medianCost > 0 {
		if f := current.TotalCostUSD / medianCost; f > factor {
			factor = f
		}
	}
	if medianInput > 0 {
		if f := float64(current.InputTokens) / medianInput; f > factor {
			factor = f
		}
	}
	if factor < RegressionFactor {
		return nil, nil
	}

	res := &RegressionResult{Factor: factor}
	if tracker != nil {
		cur := measureOverhead(st, project, projectPath, taskDesc, gates)
		res.Hint = tracker.hint(overheadKey(project, sessionName), cur)
	}
	return res, nil
}

// PromptOverhead is the size of the extra context the manager itself
// prepends ahead of a fresh run's own conversation: the context primer
// (LN-05), the descriptions of every approved skill (LN-10 — a skill's
// description is what stays permanently in a session's context; see
// internal/analysis/skill.go's RenderSkillMarkdown doc comment) and the
// project journal's tail (LN-06). A silently growing overhead is exactly
// the kind of cost regression LEARN-TASKS.md invariant 5 says a
// prompt-changing feature must stay measurable against.
type PromptOverhead struct {
	PrimerChars  int
	SkillsCount  int
	SkillsChars  int
	JournalChars int
}

func (o PromptOverhead) total() int { return o.PrimerChars + o.SkillsChars + o.JournalChars }

// OverheadTracker remembers each session's last-measured PromptOverhead in
// memory only — the "grew since last time" comparison only needs to survive
// within one running app instance, not across restarts, so this adds no new
// persisted state (no SQLite column, no state file).
type OverheadTracker struct {
	mu   sync.Mutex
	last map[string]PromptOverhead
}

// NewOverheadTracker returns an empty tracker, one per running app instance.
func NewOverheadTracker() *OverheadTracker {
	return &OverheadTracker{last: make(map[string]PromptOverhead)}
}

// hint compares cur against whatever was last recorded for key (empty on
// the first observation) and always records cur for next time, so growth is
// always measured against the *previous* observation, not an ever-growing
// baseline.
func (t *OverheadTracker) hint(key string, cur PromptOverhead) string {
	t.mu.Lock()
	prev, ok := t.last[key]
	t.last[key] = cur
	t.mu.Unlock()

	if !ok || cur.total() <= prev.total() {
		return ""
	}

	var grown []string
	if cur.PrimerChars > prev.PrimerChars {
		grown = append(grown, fmt.Sprintf("primer %d→%d chars", prev.PrimerChars, cur.PrimerChars))
	}
	if cur.SkillsChars > prev.SkillsChars {
		grown = append(grown, fmt.Sprintf("%d skill description(s) %d→%d chars", cur.SkillsCount, prev.SkillsChars, cur.SkillsChars))
	}
	if cur.JournalChars > prev.JournalChars {
		grown = append(grown, fmt.Sprintf("journal tail %d→%d chars", prev.JournalChars, cur.JournalChars))
	}
	if len(grown) == 0 {
		return fmt.Sprintf("manager prompt overhead grew from %d to %d chars", prev.total(), cur.total())
	}
	return "manager prompt overhead grew: " + strings.Join(grown, "; ")
}

func overheadKey(project, session string) string { return project + "/" + session }

// measureOverhead reads the current state of everything PromptOverhead
// tracks. Best-effort throughout: a store/read failure yields a zero value
// for that one component rather than failing the whole regression check —
// this is explanatory text for a UI banner, not a correctness-critical path.
func measureOverhead(st *store.Store, project, projectPath, taskDesc string, gates []string) PromptOverhead {
	var o PromptOverhead

	o.PrimerChars = len(BuildPrimer(PrimerInput{
		Project:     project,
		ProjectPath: projectPath,
		TaskDesc:    taskDesc,
		Gates:       gates,
		Store:       st,
	}))

	if st != nil {
		if skills, err := st.ListSkills(project); err == nil {
			for _, sk := range skills {
				if sk.Status != "approved" {
					continue
				}
				var draft analysis.SkillDraft
				if err := json.Unmarshal([]byte(sk.DraftJSON), &draft); err != nil || draft.Description == "" {
					continue
				}
				o.SkillsCount++
				o.SkillsChars += len(draft.Description)
			}
		}
	}

	if projectPath != "" {
		if entries, err := LastEntries(projectPath, journalTailEntries); err == nil {
			for _, e := range entries {
				o.JournalChars += len(e.render())
			}
		}
	}

	return o
}
