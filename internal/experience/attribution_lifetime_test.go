package experience

import (
	"math"
	"testing"
	"time"

	"claude-manager/internal/store"
)

// LEARN-TASKS.md LN-29: a tool result is re-read on every later call of its
// session, so a small result early in a long session can cost more than a
// big one at its end. The report must rank by that, not by volume.
func TestBuildAttributionReport_RanksByReReadCost(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	rows := []store.ActionRow{{
		Project: "proj", Session: "S1", CLISessionID: "cli-1", StepIndex: 0, Tool: "Bash",
		Sig: "Bash:git pull", ResultChars: 2000, Timestamp: now, // 500 tokens, 99 steps after it
	}}
	for i := 1; i < 99; i++ {
		rows = append(rows, store.ActionRow{
			Project: "proj", Session: "S1", CLISessionID: "cli-1", StepIndex: i, Tool: "Edit",
			Sig: "Edit:src/*.rs", ResultChars: 0, Timestamp: now,
		})
	}
	rows = append(rows, store.ActionRow{
		Project: "proj", Session: "S1", CLISessionID: "cli-1", StepIndex: 99, Tool: "Read",
		Sig: "Read:src/*.rs", ResultChars: 8000, Timestamp: now, // 2000 tokens, last step
	})
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	report, err := BuildAttributionReport(s, "proj", 0)
	if err != nil {
		t.Fatalf("BuildAttributionReport: %v", err)
	}
	top := report.BySignature[0]
	if top.Sig != "Bash:git pull" {
		t.Fatalf("the early result re-read 99 times must rank first, got %+v", report.BySignature)
	}
	if want := int64(500 * (cacheWriteWeight + cacheReadWeight*99)); top.LifetimeTokens != want {
		t.Errorf("LifetimeTokens = %d, want %d (500 × (2 + 0.1 × 99))", top.LifetimeTokens, want)
	}
	if read := report.BySignature[1]; read.Sig != "Read:src/*.rs" || read.LifetimeTokens != 4000 {
		t.Errorf("the last step is written once and never re-read: want Read:src/*.rs at 4000, got %+v", read)
	}
	if read := report.BySignature[1]; read.EstTokens <= top.EstTokens {
		t.Errorf("fixture: by volume the Read must be bigger (%d vs %d)", read.EstTokens, top.EstTokens)
	}
	var sum float64
	for _, row := range report.BySignature {
		sum += row.LifetimeShare
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("lifetime shares must add up to 1, got %v", sum)
	}
	if report.ByTool[0].Tool != "Bash" {
		t.Errorf("ByTool must rank by re-read cost too, got %+v", report.ByTool)
	}
}

// Steps of another CLI session (the next run of the same manager session)
// must not count as re-reads: each process starts a fresh conversation.
func TestBuildAttributionReport_RemainingStepsStayInsideOneCLISession(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	rows := []store.ActionRow{{
		Project: "proj", Session: "S1", CLISessionID: "cli-1", StepIndex: 0, Tool: "Read",
		Sig: "Read:a.md", ResultChars: 4000, Timestamp: now,
	}}
	for i := 0; i < 50; i++ {
		rows = append(rows, store.ActionRow{
			Project: "proj", Session: "S1", CLISessionID: "cli-2", StepIndex: i, Tool: "Edit",
			Sig: "Edit:b.go", ResultChars: 0, Timestamp: now,
		})
	}
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}
	report, err := BuildAttributionReport(s, "proj", 0)
	if err != nil {
		t.Fatalf("BuildAttributionReport: %v", err)
	}
	for _, row := range report.BySignature {
		if row.Sig == "Read:a.md" && row.LifetimeTokens != 2000 {
			t.Errorf("the only step of cli-1 has nothing after it: want 1000 × 2 = 2000, got %d", row.LifetimeTokens)
		}
	}
}

func TestBuildAttributionReport_ByLength(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	runs := []struct {
		turns int
		cost  float64
	}{
		{10, 0.5}, {15, 0.5}, // 1-20
		{120, 5}, // 101-150
		{300, 24}, // 251+
		{0, 3},    // no turn count: left out
		{60, 0},   // no cost: left out
	}
	for _, r := range runs {
		if err := s.InsertRun(&store.SessionRun{
			Project: "proj", Session: "S2", StartedAt: now, Status: "completed",
			NumTurns: r.turns, TotalCostUSD: r.cost,
		}); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
	}
	if err := s.InsertRun(&store.SessionRun{
		Project: "other", Session: "S2", StartedAt: now, Status: "completed", NumTurns: 10, TotalCostUSD: 99,
	}); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	report, err := BuildAttributionReport(s, "proj", 0)
	if err != nil {
		t.Fatalf("BuildAttributionReport: %v", err)
	}
	if len(report.ByLength) != 3 {
		t.Fatalf("want 3 non-empty bands, got %+v", report.ByLength)
	}
	short, mid, long := report.ByLength[0], report.ByLength[1], report.ByLength[2]
	if short.MinTurns != 1 || short.MaxTurns != 20 || short.Runs != 2 || short.CostUSD != 1 {
		t.Errorf("1-20 band = %+v, want 2 runs / $1", short)
	}
	if mid.MinTurns != 101 || mid.Runs != 1 || mid.AvgCostUSD != 5 {
		t.Errorf("101-150 band = %+v", mid)
	}
	if long.MinTurns != 251 || long.MaxTurns != 0 || math.Abs(long.CostShare-24.0/30) > 1e-9 {
		t.Errorf("251+ band = %+v, want 80%% of the $30", long)
	}
}
