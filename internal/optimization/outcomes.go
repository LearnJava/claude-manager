package optimization

import "fmt"

// MinOutcomeRuns is the minimum number of finished runs a (model, effort)
// combination needs before its measured outcome is trusted enough to steer
// a recommendation (LEARN-TASKS.md LN-13).
const MinOutcomeRuns = 5

// OutcomeCompletedThreshold is the minimum completed-rate a cheaper,
// lower-tier model must clear before it is recommended in place of the
// complexity table's default.
const OutcomeCompletedThreshold = 0.9

// OutcomeStats aggregates how one (project, complexity, model, effort)
// combination has actually performed, measured from finished session runs.
// See Store.OutcomeStats (internal/store/store.go) for how — and with what
// limitations — the real implementation computes this.
type OutcomeStats struct {
	Project    string
	Complexity Complexity
	Model      string
	Effort     string
	Runs       int
	Completed  int
	AvgCostUSD float64
	AvgTurns   float64
}

// CompletedRate is Completed/Runs, or 0 when there are no runs yet.
func (s OutcomeStats) CompletedRate() float64 {
	if s.Runs == 0 {
		return 0
	}
	return float64(s.Completed) / float64(s.Runs)
}

// OutcomeProvider supplies measured outcome stats for a project at a given
// complexity tier, one row per (model, effort) combination actually used.
// Implemented by store.Store; kept as an interface here so this package
// never needs to import internal/store, and so tests can supply a stub.
type OutcomeProvider interface {
	OutcomeStats(project string, complexity Complexity) ([]OutcomeStats, error)
}

// modelTier ranks the model aliases the routing table recommends, low to
// high. Unranked/custom model ids (a pinned id, Fable, an unrecognised
// string) return -1 and are never treated as "lower" than anything, and
// never considered as a downgrade target — an override must only ever move
// to a model this ladder actually knows is cheaper.
func modelTier(model string) int {
	switch model {
	case "haiku":
		return 0
	case "sonnet":
		return 1
	case "opus":
		return 2
	default:
		return -1
	}
}

// mergeByModel collapses per-effort rows into one OutcomeStats per model:
// Runs/Completed sum, AvgCostUSD/AvgTurns become run-weighted averages, and
// Effort is the effort of whichever input row had the most runs (a
// recommendation has to name one concrete effort to launch with).
func mergeByModel(stats []OutcomeStats) []OutcomeStats {
	type acc struct {
		out            OutcomeStats
		costSum        float64
		turnsSum       float64
		bestEffortRuns int
	}
	byModel := map[string]*acc{}
	var order []string
	for _, s := range stats {
		a, ok := byModel[s.Model]
		if !ok {
			a = &acc{out: OutcomeStats{Project: s.Project, Complexity: s.Complexity, Model: s.Model}}
			byModel[s.Model] = a
			order = append(order, s.Model)
		}
		a.out.Runs += s.Runs
		a.out.Completed += s.Completed
		a.costSum += s.AvgCostUSD * float64(s.Runs)
		a.turnsSum += s.AvgTurns * float64(s.Runs)
		if s.Runs > a.bestEffortRuns {
			a.bestEffortRuns = s.Runs
			a.out.Effort = s.Effort
		}
	}
	merged := make([]OutcomeStats, 0, len(order))
	for _, m := range order {
		a := byModel[m]
		if a.out.Runs > 0 {
			a.out.AvgCostUSD = a.costSum / float64(a.out.Runs)
			a.out.AvgTurns = a.turnsSum / float64(a.out.Runs)
		}
		merged = append(merged, a.out)
	}
	return merged
}

// evaluateOutcome decides whether measured outcomes justify downgrading away
// from baseModel: it requires measured data for baseModel itself (so
// "cheaper" has something to compare against) and a strictly lower-tier
// model with >= MinOutcomeRuns runs, a completed rate clearing
// OutcomeCompletedThreshold, and a measured average cost below baseModel's.
// Ties between qualifying models favor the higher (closer to baseModel)
// tier, then the cheaper one. ok is false when nothing qualifies — Route
// must fall back to the unmodified base recommendation in that case.
func evaluateOutcome(stats []OutcomeStats, baseModel string) (cand OutcomeStats, baseCost float64, ok bool) {
	baseTier := modelTier(baseModel)
	if baseTier <= 0 {
		return OutcomeStats{}, 0, false // already bottom tier (or unranked) — nothing lower to try
	}

	merged := mergeByModel(stats)
	var baseFound bool
	for _, agg := range merged {
		if agg.Model == baseModel {
			baseCost, baseFound = agg.AvgCostUSD, true
			break
		}
	}
	if !baseFound {
		return OutcomeStats{}, 0, false
	}

	var best *OutcomeStats
	for i := range merged {
		agg := &merged[i]
		tier := modelTier(agg.Model)
		if tier < 0 || tier >= baseTier {
			continue
		}
		if agg.Runs < MinOutcomeRuns {
			continue
		}
		if agg.CompletedRate() < OutcomeCompletedThreshold {
			continue
		}
		if agg.AvgCostUSD >= baseCost {
			continue
		}
		if best == nil || tier > modelTier(best.Model) ||
			(tier == modelTier(best.Model) && agg.AvgCostUSD < best.AvgCostUSD) {
			best = agg
		}
	}
	if best == nil {
		return OutcomeStats{}, baseCost, false
	}
	return *best, baseCost, true
}

// outcomeReason renders the human-readable explanation ModelPicker shows,
// e.g. "на основании 7 прогонов: sonnet 7/7 completed, дешевле opus в 4.1×".
func outcomeReason(cand OutcomeStats, baseModel string, baseCost float64) string {
	factor := 0.0
	if cand.AvgCostUSD > 0 {
		factor = baseCost / cand.AvgCostUSD
	}
	return fmt.Sprintf("на основании %d прогонов: %s %d/%d completed, дешевле %s в %.1f×",
		cand.Runs, cand.Model, cand.Completed, cand.Runs, baseModel, factor)
}
