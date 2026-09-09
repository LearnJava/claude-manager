package experience

import (
	"testing"
	"time"

	"claude-manager/internal/store"
)

// mkHistoryRun inserts one already-finished session_runs row for project/S1
// at cost/inputTokens, older than the runs mkCurrentRun will insert next —
// StartedAt only needs to keep ListRuns' newest-first ordering stable, so
// each call in a test nudges it back by a minute.
func mkHistoryRun(t *testing.T, s *store.Store, project string, when time.Time, cost float64, inputTokens int64) *store.SessionRun {
	t.Helper()
	r := &store.SessionRun{
		Project: project, Session: "S1", Model: "sonnet",
		StartedAt: when, Status: "completed",
		TotalCostUSD: cost, InputTokens: inputTokens,
	}
	if err := s.InsertRun(r); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	return r
}

// TestDetectRegression_Growth: a run 3x the baseline median cost fires,
// with Factor reporting the actual ratio (LEARN-TASKS.md LN-16's "рост"
// case).
func TestDetectRegression_Growth(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		mkHistoryRun(t, s, "proj", base.Add(time.Duration(-i-1)*time.Hour), 1.0, 10000)
	}
	cur := mkHistoryRun(t, s, "proj", base, 3.0, 10000)

	res, err := DetectRegression(s, nil, "proj", "S1", "", "", nil, cur.ID)
	if err != nil {
		t.Fatalf("DetectRegression: %v", err)
	}
	if res == nil {
		t.Fatal("DetectRegression = nil, want a regression (3x median cost)")
	}
	if res.Factor < 2.9 || res.Factor > 3.1 {
		t.Errorf("Factor = %v, want ~3.0", res.Factor)
	}
}

// TestDetectRegression_InputTokenGrowth: cost stays flat but input tokens
// more than double — the "или входной контекст на старте вырос вдвое"
// clause fires independently of the cost check.
func TestDetectRegression_InputTokenGrowth(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		mkHistoryRun(t, s, "proj", base.Add(time.Duration(-i-1)*time.Hour), 1.0, 10000)
	}
	cur := mkHistoryRun(t, s, "proj", base, 1.05, 25000)

	res, err := DetectRegression(s, nil, "proj", "S1", "", "", nil, cur.ID)
	if err != nil {
		t.Fatalf("DetectRegression: %v", err)
	}
	if res == nil {
		t.Fatal("DetectRegression = nil, want a regression (2.5x median input tokens)")
	}
}

// TestDetectRegression_Noise: values fluctuate around the median but never
// cross RegressionFactor — must not fire (LEARN-TASKS.md LN-16's "шум"
// case).
func TestDetectRegression_Noise(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	costs := []float64{0.9, 1.1, 1.0, 1.2, 0.95}
	for i, c := range costs {
		mkHistoryRun(t, s, "proj", base.Add(time.Duration(-i-1)*time.Hour), c, 10000)
	}
	cur := mkHistoryRun(t, s, "proj", base, 1.8, 11000) // under 2x the ~1.0 median

	res, err := DetectRegression(s, nil, "proj", "S1", "", "", nil, cur.ID)
	if err != nil {
		t.Fatalf("DetectRegression: %v", err)
	}
	if res != nil {
		t.Errorf("DetectRegression = %+v, want nil (within noise band)", res)
	}
}

// TestDetectRegression_InsufficientHistory: fewer than MinRegressionHistory
// prior runs must never fire, however large the jump — a median over one or
// two runs is not a trend (LEARN-TASKS.md LN-16's "мало данных" case).
func TestDetectRegression_InsufficientHistory(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	mkHistoryRun(t, s, "proj", base.Add(-time.Hour), 1.0, 10000)
	cur := mkHistoryRun(t, s, "proj", base, 10.0, 100000)

	res, err := DetectRegression(s, nil, "proj", "S1", "", "", nil, cur.ID)
	if err != nil {
		t.Fatalf("DetectRegression: %v", err)
	}
	if res != nil {
		t.Errorf("DetectRegression = %+v, want nil (only 1 prior run)", res)
	}
}

// TestDetectRegression_OtherSessionIgnored: history is scoped to the same
// session — a spike in a sibling session's runs must never inflate this
// session's baseline (or count toward MinRegressionHistory).
func TestDetectRegression_OtherSessionIgnored(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		r := &store.SessionRun{
			Project: "proj", Session: "S2", Model: "sonnet",
			StartedAt: base.Add(time.Duration(-i-1) * time.Hour), Status: "completed",
			TotalCostUSD: 50.0, InputTokens: 500000,
		}
		if err := s.InsertRun(r); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
	}
	cur := mkHistoryRun(t, s, "proj", base, 1.0, 10000)

	res, err := DetectRegression(s, nil, "proj", "S1", "", "", nil, cur.ID)
	if err != nil {
		t.Fatalf("DetectRegression: %v", err)
	}
	if res != nil {
		t.Errorf("DetectRegression = %+v, want nil (no S1 history at all)", res)
	}
}

// TestOverheadTracker_Hint: the first observation never produces a hint (no
// baseline yet); a later observation whose total grew produces one that
// names what grew; a later one whose total didn't grow produces none.
func TestOverheadTracker_Hint(t *testing.T) {
	tr := NewOverheadTracker()

	if h := tr.hint("proj/S1", PromptOverhead{PrimerChars: 500}); h != "" {
		t.Errorf("first observation hint = %q, want \"\"", h)
	}

	h := tr.hint("proj/S1", PromptOverhead{PrimerChars: 500, SkillsCount: 2, SkillsChars: 300})
	if h == "" {
		t.Fatal("hint = \"\", want a non-empty explanation (overhead grew 500 -> 800)")
	}

	// Same total again — no growth, no hint.
	if h := tr.hint("proj/S1", PromptOverhead{PrimerChars: 500, SkillsCount: 2, SkillsChars: 300}); h != "" {
		t.Errorf("unchanged overhead hint = %q, want \"\"", h)
	}

	// A different session key has its own independent baseline.
	if h := tr.hint("proj/S2", PromptOverhead{PrimerChars: 10}); h != "" {
		t.Errorf("first observation for a different key hint = %q, want \"\"", h)
	}
}

// TestDetectRegression_HintReflectsSkillGrowth: an end-to-end check that a
// regression's Hint reports newly-approved skill descriptions once a
// tracker has a prior baseline to compare against.
func TestDetectRegression_HintReflectsSkillGrowth(t *testing.T) {
	s := newTestStore(t)
	tr := NewOverheadTracker()
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		mkHistoryRun(t, s, "proj", base.Add(time.Duration(-i-1)*time.Hour), 1.0, 10000)
	}

	// First finished run past the threshold: no approved skills yet, and no
	// prior overhead measurement, so no hint.
	cur1 := mkHistoryRun(t, s, "proj", base, 3.0, 10000)
	res1, err := DetectRegression(s, tr, "proj", "S1", "", "", nil, cur1.ID)
	if err != nil {
		t.Fatalf("DetectRegression (1): %v", err)
	}
	if res1 == nil || res1.Hint != "" {
		t.Fatalf("first regression = %+v, want a regression with an empty Hint", res1)
	}

	// Approve a skill with a sizeable description between the two checks.
	sk := &store.Skill{
		Project: "proj", Name: "example-skill", Status: "draft",
		DraftJSON:  `{"name":"example-skill","description":"this is a long enough description to move the total"}`,
		MD:         "body",
		SourceJSON: "[]",
		CreatedAt:  base,
	}
	if err := s.InsertSkill(sk); err != nil {
		t.Fatalf("InsertSkill: %v", err)
	}
	if err := s.UpdateSkillApproved(sk.ID, "body", base); err != nil {
		t.Fatalf("UpdateSkillApproved: %v", err)
	}

	cur2 := mkHistoryRun(t, s, "proj", base.Add(time.Hour), 3.0, 10000)
	res2, err := DetectRegression(s, tr, "proj", "S1", "", "", nil, cur2.ID)
	if err != nil {
		t.Fatalf("DetectRegression (2): %v", err)
	}
	if res2 == nil {
		t.Fatal("second regression = nil, want a regression")
	}
	if res2.Hint == "" {
		t.Error("second regression Hint is empty, want it to report the newly-approved skill description")
	}
}
