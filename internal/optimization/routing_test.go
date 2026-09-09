package optimization

import (
	"errors"
	"strings"
	"testing"
)

var errBoom = errors.New("boom")

// stubOutcomeProvider is a table-driven test double for OutcomeProvider: it
// returns a canned stats slice (or error) regardless of the arguments, which
// is all Route's tests need — the interesting behavior lives in
// applyOutcomeOverride/evaluateOutcome, not in how a real provider queries.
type stubOutcomeProvider struct {
	stats []OutcomeStats
	err   error
}

func (p *stubOutcomeProvider) OutcomeStats(project string, complexity Complexity) ([]OutcomeStats, error) {
	return p.stats, p.err
}

func TestRoute_NoProvider_UnchangedFromComplexityTable(t *testing.T) {
	r := NewModelRouter(nil)
	for _, c := range []Complexity{ComplexityTrivial, ComplexityStandard, ComplexityComplex, ComplexityArchitectural} {
		want := routeByComplexity(c)
		got := r.Route("proj", "", "", string(c))
		if got != want {
			t.Errorf("complexity %s: Route()=%+v, routeByComplexity()=%+v", c, got, want)
		}
	}
}

func TestRoute_NoProvider_AnalystRecommendationUnchanged(t *testing.T) {
	r := NewModelRouter(nil)
	got := r.Route("proj", "opus", "high", "standard")
	want := ModelRecommendation{Model: "opus", Effort: "high", Complexity: "standard", Reason: "Recommended by pre-flight analysis"}
	if got != want {
		t.Errorf("Route()=%+v, want %+v", got, want)
	}
}

func TestRoute_EmptyProject_ProviderNeverConsulted(t *testing.T) {
	r := NewModelRouter(nil)
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "sonnet", Effort: "medium", Runs: 50, Completed: 50, AvgCostUSD: 0.01},
	}})
	want := routeByComplexity(ComplexityStandard)
	got := r.Route("", "", "", string(ComplexityStandard)) // no project → provider must not fire
	if got != want {
		t.Errorf("Route() with empty project=%+v, want unmodified %+v", got, want)
	}
}

func TestRoute_ProviderError_FallsBackToBase(t *testing.T) {
	r := NewModelRouter(nil)
	r.SetOutcomeProvider(&stubOutcomeProvider{err: errBoom})
	want := routeByComplexity(ComplexityStandard)
	got := r.Route("proj", "", "", string(ComplexityStandard))
	if got != want {
		t.Errorf("Route() on provider error=%+v, want %+v", got, want)
	}
}

func TestRoute_InsufficientRuns_NoOverride(t *testing.T) {
	r := NewModelRouter(nil)
	// standard → base model "sonnet" per the static table. Candidate "haiku"
	// would otherwise qualify (cheap, 100% completed) but has only 4 runs.
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "sonnet", Effort: "medium", Runs: 20, Completed: 20, AvgCostUSD: 0.05},
		{Model: "haiku", Effort: "low", Runs: MinOutcomeRuns - 1, Completed: MinOutcomeRuns - 1, AvgCostUSD: 0.01},
	}})
	want := routeByComplexity(ComplexityStandard)
	got := r.Route("proj", "", "", string(ComplexityStandard))
	if got != want {
		t.Errorf("Route() with insufficient candidate runs=%+v, want unmodified %+v", got, want)
	}
}

func TestRoute_LowCompletedRate_NoOverride(t *testing.T) {
	r := NewModelRouter(nil)
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "sonnet", Effort: "medium", Runs: 20, Completed: 20, AvgCostUSD: 0.05},
		{Model: "haiku", Effort: "low", Runs: 10, Completed: 8, AvgCostUSD: 0.01}, // 80% < 90% threshold
	}})
	want := routeByComplexity(ComplexityStandard)
	got := r.Route("proj", "", "", string(ComplexityStandard))
	if got != want {
		t.Errorf("Route() with 80%% completed rate=%+v, want unmodified %+v", got, want)
	}
}

func TestRoute_NotCheaper_NoOverride(t *testing.T) {
	r := NewModelRouter(nil)
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "sonnet", Effort: "medium", Runs: 20, Completed: 20, AvgCostUSD: 0.01},
		{Model: "haiku", Effort: "low", Runs: 10, Completed: 10, AvgCostUSD: 0.02}, // more expensive
	}})
	want := routeByComplexity(ComplexityStandard)
	got := r.Route("proj", "", "", string(ComplexityStandard))
	if got != want {
		t.Errorf("Route() with a pricier candidate=%+v, want unmodified %+v", got, want)
	}
}

func TestRoute_NoBaseData_NoOverride(t *testing.T) {
	r := NewModelRouter(nil)
	// Only the candidate has data — nothing to compare its cost against.
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "haiku", Effort: "low", Runs: 10, Completed: 10, AvgCostUSD: 0.01},
	}})
	want := routeByComplexity(ComplexityStandard)
	got := r.Route("proj", "", "", string(ComplexityStandard))
	if got != want {
		t.Errorf("Route() with no base-model data=%+v, want unmodified %+v", got, want)
	}
}

func TestRoute_QualifyingOutcome_Downgrades(t *testing.T) {
	r := NewModelRouter(nil)
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "sonnet", Effort: "medium", Runs: 20, Completed: 20, AvgCostUSD: 0.041},
		{Model: "haiku", Effort: "low", Runs: 7, Completed: 7, AvgCostUSD: 0.01},
	}})
	got := r.Route("proj", "", "", string(ComplexityStandard))
	if got.Model != "haiku" || got.Effort != "low" {
		t.Fatalf("Route()=%+v, want downgrade to haiku/low", got)
	}
	if got.Complexity != string(ComplexityStandard) {
		t.Errorf("Complexity=%q, want unchanged %q", got.Complexity, ComplexityStandard)
	}
	if !strings.Contains(got.Reason, "7") || !strings.Contains(got.Reason, "haiku") || !strings.Contains(got.Reason, "sonnet") {
		t.Errorf("Reason=%q missing run count / model names", got.Reason)
	}
}

func TestRoute_Architectural_NeverDowngraded(t *testing.T) {
	r := NewModelRouter(nil)
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "opus", Effort: "high", Runs: 20, Completed: 20, AvgCostUSD: 0.5},
		{Model: "sonnet", Effort: "high", Runs: 20, Completed: 20, AvgCostUSD: 0.1},
	}})
	want := routeByComplexity(ComplexityArchitectural)
	got := r.Route("proj", "", "", string(ComplexityArchitectural))
	if got != want {
		t.Errorf("Route() for architectural=%+v, want unmodified %+v", got, want)
	}
}

func TestRoute_HaikuBase_NeverDowngradedFurther(t *testing.T) {
	r := NewModelRouter(nil)
	// trivial → base "haiku", already the bottom tier; nothing cheaper exists
	// in the ladder even if some custom/unranked model had great stats.
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "haiku", Effort: "low", Runs: 20, Completed: 20, AvgCostUSD: 0.01},
		{Model: "some-pinned-id", Effort: "low", Runs: 20, Completed: 20, AvgCostUSD: 0.001},
	}})
	want := routeByComplexity(ComplexityTrivial)
	got := r.Route("proj", "", "", string(ComplexityTrivial))
	if got != want {
		t.Errorf("Route() for trivial=%+v, want unmodified %+v", got, want)
	}
}

func TestRoute_HigherTierCandidate_NoOverride(t *testing.T) {
	r := NewModelRouter(nil)
	// complex → base "sonnet". opus has great stats but is a *higher* tier,
	// never a downgrade target.
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "sonnet", Effort: "high", Runs: 20, Completed: 20, AvgCostUSD: 0.1},
		{Model: "opus", Effort: "high", Runs: 20, Completed: 20, AvgCostUSD: 0.01},
	}})
	want := routeByComplexity(ComplexityComplex)
	got := r.Route("proj", "", "", string(ComplexityComplex))
	if got != want {
		t.Errorf("Route() with only a higher-tier candidate=%+v, want unmodified %+v", got, want)
	}
}

func TestRoute_MultiEffortRows_MergedPerModel(t *testing.T) {
	r := NewModelRouter(nil)
	// haiku's runs split across two efforts; individually under
	// MinOutcomeRuns, but merged they clear it and the majority effort wins.
	r.SetOutcomeProvider(&stubOutcomeProvider{stats: []OutcomeStats{
		{Model: "sonnet", Effort: "medium", Runs: 20, Completed: 20, AvgCostUSD: 0.05},
		{Model: "haiku", Effort: "low", Runs: 4, Completed: 4, AvgCostUSD: 0.01},
		{Model: "haiku", Effort: "medium", Runs: 2, Completed: 2, AvgCostUSD: 0.015},
	}})
	got := r.Route("proj", "", "", string(ComplexityStandard))
	if got.Model != "haiku" || got.Effort != "low" {
		t.Fatalf("Route()=%+v, want merged haiku/low (majority effort)", got)
	}
}
