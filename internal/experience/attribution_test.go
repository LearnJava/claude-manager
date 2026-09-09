package experience

import (
	"testing"
	"time"

	"claude-manager/internal/store"
)

// TestEstimateTokens covers the chars→tokens approximation's edge cases
// (LEARN-TASKS.md LN-12): zero/negative chars must estimate to 0, never a
// negative or garbage value, since a caller sums these across many rows.
func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		chars int
		want  int
	}{
		{0, 0},
		{-100, 0},
		{4, 1},
		{3, 0}, // integer division rounds down
		{4000, 1000},
		{5809, 1452}, // the measured lumen Read average from LEARN-TASKS.md LN-12
	}
	for _, c := range cases {
		if got := EstimateTokens(c.chars); got != c.want {
			t.Errorf("EstimateTokens(%d) = %d, want %d", c.chars, got, c.want)
		}
	}
}

// TestBuildAttributionReport_SignatureAndToolCuts seeds one project with a
// dominant Read signature and a smaller Bash one, and checks both the
// per-signature and per-tool cuts, the ordering (most expensive first), and
// that Share sums correctly across the whole report (LEARN-TASKS.md LN-12:
// "тесты оценщика и агрегатора").
func TestBuildAttributionReport_SignatureAndToolCuts(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	var rows []store.ActionRow
	// Read of one file: 10 calls, 4000 chars each = 1000 est tokens each,
	// 10000 total.
	for i := 0; i < 10; i++ {
		rows = append(rows, store.ActionRow{
			Project: "proj", Session: "S1", StepIndex: i, Tool: "Read",
			Sig: "Read:src/*.go", Arg: "src/main.go", ResultChars: 4000,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}
	// Bash git status: 5 calls, 400 chars each = 100 est tokens each, 500
	// total.
	for i := 0; i < 5; i++ {
		rows = append(rows, store.ActionRow{
			Project: "proj", Session: "S1", StepIndex: 100 + i, Tool: "Bash",
			Sig: "Bash:git status", Arg: "git status", ResultChars: 400,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}
	// A second project must not leak into "proj"'s report.
	rows = append(rows, store.ActionRow{
		Project: "other", Session: "S1", StepIndex: 0, Tool: "Read",
		Sig: "Read:other/*.go", ResultChars: 999999, Timestamp: now,
	})
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	report, err := BuildAttributionReport(s, "proj", 0)
	if err != nil {
		t.Fatalf("BuildAttributionReport: %v", err)
	}

	if report.TotalEstTokens != 10500 {
		t.Errorf("TotalEstTokens = %d, want 10500", report.TotalEstTokens)
	}

	if len(report.BySignature) != 2 {
		t.Fatalf("BySignature has %d entries, want 2: %+v", len(report.BySignature), report.BySignature)
	}
	top := report.BySignature[0]
	if top.Sig != "Read:src/*.go" {
		t.Errorf("top signature = %q, want Read:src/*.go (most expensive first)", top.Sig)
	}
	if top.Count != 10 {
		t.Errorf("top.Count = %d, want 10", top.Count)
	}
	if top.EstTokens != 10000 {
		t.Errorf("top.EstTokens = %d, want 10000", top.EstTokens)
	}
	if top.AvgResultChars != 4000 {
		t.Errorf("top.AvgResultChars = %v, want 4000", top.AvgResultChars)
	}
	if top.MaxResultChars != 4000 {
		t.Errorf("top.MaxResultChars = %d, want 4000", top.MaxResultChars)
	}
	wantShare := 10000.0 / 10500.0
	if diff := top.Share - wantShare; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("top.Share = %v, want %v", top.Share, wantShare)
	}

	second := report.BySignature[1]
	if second.Sig != "Bash:git status" {
		t.Errorf("second signature = %q, want Bash:git status", second.Sig)
	}
	if second.EstTokens != 500 {
		t.Errorf("second.EstTokens = %d, want 500", second.EstTokens)
	}

	if len(report.ByTool) != 2 {
		t.Fatalf("ByTool has %d entries, want 2: %+v", len(report.ByTool), report.ByTool)
	}
	if report.ByTool[0].Tool != "Read" || report.ByTool[0].EstTokens != 10000 {
		t.Errorf("ByTool[0] = %+v, want Read/10000", report.ByTool[0])
	}
	if report.ByTool[1].Tool != "Bash" || report.ByTool[1].EstTokens != 500 {
		t.Errorf("ByTool[1] = %+v, want Bash/500", report.ByTool[1])
	}
}

// TestBuildAttributionReport_TopNLimit checks that topN truncates
// BySignature but never ByTool, and that a truncated report's Share still
// reflects the whole project's volume (the denominator), not just the
// reported rows.
func TestBuildAttributionReport_TopNLimit(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	var rows []store.ActionRow
	sigs := []struct {
		sig   string
		chars int
	}{
		{"Read:a/*.go", 4000},
		{"Read:b/*.go", 3000},
		{"Read:c/*.go", 2000},
		{"Read:d/*.go", 1000},
	}
	for i, sc := range sigs {
		rows = append(rows, store.ActionRow{
			Project: "proj", Session: "S1", StepIndex: i, Tool: "Read",
			Sig: sc.sig, ResultChars: sc.chars, Timestamp: now,
		})
	}
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	report, err := BuildAttributionReport(s, "proj", 2)
	if err != nil {
		t.Fatalf("BuildAttributionReport: %v", err)
	}
	if len(report.BySignature) != 2 {
		t.Fatalf("BySignature has %d entries, want 2 (topN)", len(report.BySignature))
	}
	if report.BySignature[0].Sig != "Read:a/*.go" || report.BySignature[1].Sig != "Read:b/*.go" {
		t.Errorf("BySignature = %+v, want a then b (most expensive first)", report.BySignature)
	}
	// TotalEstTokens (and therefore Share) must reflect all 4 signatures, not
	// just the top 2 kept in BySignature.
	wantTotal := int64(EstimateTokens(4000) + EstimateTokens(3000) + EstimateTokens(2000) + EstimateTokens(1000))
	if report.TotalEstTokens != wantTotal {
		t.Errorf("TotalEstTokens = %d, want %d (all signatures, not just top-2)", report.TotalEstTokens, wantTotal)
	}
	if len(report.ByTool) != 1 {
		t.Errorf("ByTool has %d entries, want 1 (never truncated)", len(report.ByTool))
	}
}

// TestBuildAttributionReport_ZeroResultCharsDoesNotBreakShare covers the
// task's own explicit test requirement: calls with result_chars == 0 (no
// result ever captured) must still count toward Count, but must not turn
// Share into NaN/Inf or otherwise distort the percentages (LEARN-TASKS.md
// LN-12: "тест что вызовы без результата (result_chars = 0) не ломают
// проценты").
func TestBuildAttributionReport_ZeroResultCharsDoesNotBreakShare(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	t.Run("mixed with real results", func(t *testing.T) {
		var rows []store.ActionRow
		rows = append(rows, store.ActionRow{
			Project: "mixed", Session: "S1", StepIndex: 0, Tool: "Read",
			Sig: "Read:a/*.go", ResultChars: 4000, Timestamp: now,
		})
		// Three calls whose result never arrived.
		for i := 1; i <= 3; i++ {
			rows = append(rows, store.ActionRow{
				Project: "mixed", Session: "S1", StepIndex: i, Tool: "Bash",
				Sig: "Bash:sleep <ARG>", ResultChars: 0, Timestamp: now,
			})
		}
		if err := s.InsertActions(rows); err != nil {
			t.Fatalf("InsertActions: %v", err)
		}

		report, err := BuildAttributionReport(s, "mixed", 0)
		if err != nil {
			t.Fatalf("BuildAttributionReport: %v", err)
		}
		if report.TotalEstTokens != 1000 {
			t.Errorf("TotalEstTokens = %d, want 1000 (zero-result rows contribute nothing)", report.TotalEstTokens)
		}
		var sleep *SignatureAttribution
		for i := range report.BySignature {
			if report.BySignature[i].Sig == "Bash:sleep <ARG>" {
				sleep = &report.BySignature[i]
			}
		}
		if sleep == nil {
			t.Fatal("Bash:sleep <ARG> missing from BySignature — zero-result calls must still be counted")
		}
		if sleep.Count != 3 {
			t.Errorf("sleep.Count = %d, want 3", sleep.Count)
		}
		if sleep.EstTokens != 0 {
			t.Errorf("sleep.EstTokens = %d, want 0", sleep.EstTokens)
		}
		if sleep.Share != 0 {
			t.Errorf("sleep.Share = %v, want 0", sleep.Share)
		}
	})

	t.Run("all zero — total is zero, Share must not be NaN/Inf", func(t *testing.T) {
		rows := []store.ActionRow{
			{Project: "allzero", Session: "S1", StepIndex: 0, Tool: "Bash",
				Sig: "Bash:sleep <ARG>", ResultChars: 0, Timestamp: now},
			{Project: "allzero", Session: "S1", StepIndex: 1, Tool: "Bash",
				Sig: "Bash:sleep <ARG>", ResultChars: 0, Timestamp: now},
		}
		if err := s.InsertActions(rows); err != nil {
			t.Fatalf("InsertActions: %v", err)
		}
		report, err := BuildAttributionReport(s, "allzero", 0)
		if err != nil {
			t.Fatalf("BuildAttributionReport: %v", err)
		}
		if report.TotalEstTokens != 0 {
			t.Errorf("TotalEstTokens = %d, want 0", report.TotalEstTokens)
		}
		if len(report.BySignature) != 1 {
			t.Fatalf("BySignature has %d entries, want 1", len(report.BySignature))
		}
		if report.BySignature[0].Share != 0 {
			t.Errorf("Share = %v, want 0 (not NaN/Inf) when TotalEstTokens is 0", report.BySignature[0].Share)
		}
	})
}
