package optimization

import "testing"

func TestOutcomeStats_CompletedRate(t *testing.T) {
	cases := []struct {
		name      string
		runs      int
		completed int
		want      float64
	}{
		{"no runs", 0, 0, 0},
		{"all completed", 10, 10, 1},
		{"half completed", 10, 5, 0.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := OutcomeStats{Runs: c.runs, Completed: c.completed}
			if got := s.CompletedRate(); got != c.want {
				t.Errorf("CompletedRate()=%v, want %v", got, c.want)
			}
		})
	}
}

func TestModelTier(t *testing.T) {
	cases := map[string]int{
		"haiku":          0,
		"sonnet":         1,
		"opus":           2,
		"":               -1,
		"claude-fable-5": -1,
		"some-pinned-id": -1,
	}
	for model, want := range cases {
		if got := modelTier(model); got != want {
			t.Errorf("modelTier(%q)=%d, want %d", model, got, want)
		}
	}
}

func TestMergeByModel(t *testing.T) {
	stats := []OutcomeStats{
		{Model: "haiku", Effort: "low", Runs: 4, Completed: 4, AvgCostUSD: 0.01, AvgTurns: 2},
		{Model: "haiku", Effort: "medium", Runs: 6, Completed: 3, AvgCostUSD: 0.02, AvgTurns: 4},
		{Model: "sonnet", Effort: "medium", Runs: 10, Completed: 10, AvgCostUSD: 0.05, AvgTurns: 3},
	}
	merged := mergeByModel(stats)
	if len(merged) != 2 {
		t.Fatalf("len(merged)=%d, want 2", len(merged))
	}

	var haiku, sonnet *OutcomeStats
	for i := range merged {
		switch merged[i].Model {
		case "haiku":
			haiku = &merged[i]
		case "sonnet":
			sonnet = &merged[i]
		}
	}
	if haiku == nil || sonnet == nil {
		t.Fatalf("merged=%+v missing haiku or sonnet", merged)
	}

	if haiku.Runs != 10 || haiku.Completed != 7 {
		t.Errorf("haiku Runs/Completed=%d/%d, want 10/7", haiku.Runs, haiku.Completed)
	}
	// run-weighted average cost: (4*0.01 + 6*0.02)/10 = 0.016
	if got := haiku.AvgCostUSD; got < 0.0159 || got > 0.0161 {
		t.Errorf("haiku AvgCostUSD=%v, want ~0.016", got)
	}
	// the effort with the most runs (6, "medium") wins.
	if haiku.Effort != "medium" {
		t.Errorf("haiku Effort=%q, want %q (majority by run count)", haiku.Effort, "medium")
	}

	if sonnet.Runs != 10 || sonnet.Effort != "medium" {
		t.Errorf("sonnet=%+v, want Runs=10 Effort=medium (single row passthrough)", sonnet)
	}
}

func TestEvaluateOutcome_QualifyingCandidate(t *testing.T) {
	stats := []OutcomeStats{
		{Model: "sonnet", Effort: "medium", Runs: 20, Completed: 20, AvgCostUSD: 0.05},
		{Model: "haiku", Effort: "low", Runs: 5, Completed: 5, AvgCostUSD: 0.01},
	}
	cand, baseCost, ok := evaluateOutcome(stats, "sonnet")
	if !ok {
		t.Fatal("evaluateOutcome() ok=false, want true")
	}
	if cand.Model != "haiku" {
		t.Errorf("cand.Model=%q, want haiku", cand.Model)
	}
	if baseCost != 0.05 {
		t.Errorf("baseCost=%v, want 0.05", baseCost)
	}
}

func TestEvaluateOutcome_NoBaseModelData(t *testing.T) {
	stats := []OutcomeStats{
		{Model: "haiku", Effort: "low", Runs: 5, Completed: 5, AvgCostUSD: 0.01},
	}
	if _, _, ok := evaluateOutcome(stats, "sonnet"); ok {
		t.Error("evaluateOutcome() ok=true with no base-model data, want false")
	}
}

func TestEvaluateOutcome_BottomTierBase(t *testing.T) {
	stats := []OutcomeStats{
		{Model: "haiku", Effort: "low", Runs: 20, Completed: 20, AvgCostUSD: 0.01},
	}
	if _, _, ok := evaluateOutcome(stats, "haiku"); ok {
		t.Error("evaluateOutcome() ok=true for a haiku base (bottom tier), want false")
	}
}

func TestEvaluateOutcome_UnrankedBase(t *testing.T) {
	stats := []OutcomeStats{
		{Model: "custom-pinned-id", Effort: "", Runs: 20, Completed: 20, AvgCostUSD: 0.01},
		{Model: "haiku", Effort: "low", Runs: 20, Completed: 20, AvgCostUSD: 0.001},
	}
	if _, _, ok := evaluateOutcome(stats, "custom-pinned-id"); ok {
		t.Error("evaluateOutcome() ok=true for an unranked base model, want false")
	}
}

func TestOutcomeReason_MentionsFactorAndModels(t *testing.T) {
	cand := OutcomeStats{Model: "haiku", Runs: 7, Completed: 7, AvgCostUSD: 0.01}
	got := outcomeReason(cand, "sonnet", 0.041)
	want := "на основании 7 прогонов: haiku 7/7 completed, дешевле sonnet в 4.1×"
	if got != want {
		t.Errorf("outcomeReason()=%q, want %q", got, want)
	}
}
