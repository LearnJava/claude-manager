package session

import (
	"testing"
	"time"

	"claude-manager/internal/store"
)

// The dollar figure and the token figure are shown side by side in the UI, so
// they must be computed from the same daily_metrics rows. These tests pin that
// down — and pin down that cache traffic is counted, which is the whole reason
// the columns were added (before it, a day's token total ignored the bulk of
// what actually went through the model).

func seedDaily(t *testing.T, st *store.Store, date, project string, in, out, cacheRead, cacheCreate int64, cost float64) {
	t.Helper()
	err := st.AddDailyMetrics(&store.DailyMetrics{
		Date:                     date,
		Project:                  project,
		TotalCost:                cost,
		TotalInputTokens:         in,
		TotalOutputTokens:        out,
		TotalCacheReadTokens:     cacheRead,
		TotalCacheCreationTokens: cacheCreate,
		TotalRuns:                1,
		TotalTasks:               1,
	})
	if err != nil {
		t.Fatalf("AddDailyMetrics: %v", err)
	}
}

func TestGetDailyTokens(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	date := "2026-09-08"
	seedDaily(t, st, date, "lumen", 1000, 200, 50_000, 3000, 0.42)

	got, err := m.GetDailyTokens(date)
	if err != nil {
		t.Fatalf("GetDailyTokens: %v", err)
	}
	if got.InputTokens != 1000 || got.OutputTokens != 200 {
		t.Errorf("input/output: got %d/%d want 1000/200", got.InputTokens, got.OutputTokens)
	}
	if got.CacheRead != 50_000 || got.CacheCreation != 3000 {
		t.Errorf("cache: got %d/%d want 50000/3000", got.CacheRead, got.CacheCreation)
	}
	if want := int64(54_200); got.Total != want {
		t.Errorf("total: got %d want %d (cache traffic must be counted)", got.Total, want)
	}
	// The dollar figure travels with the token figure so the two readouts can
	// never describe different sets of runs.
	if got.CostUSD != 0.42 {
		t.Errorf("cost: got %f want 0.42", got.CostUSD)
	}
}

func TestGetDailyTokensMatchesGetDailyCost(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	date := "2026-09-08"
	seedDaily(t, st, date, "lumen", 10, 20, 30, 40, 1.25)
	seedDaily(t, st, date, "lumen", 1, 2, 3, 4, 0.75)

	cost, err := m.GetDailyCost(date)
	if err != nil {
		t.Fatalf("GetDailyCost: %v", err)
	}
	tok, err := m.GetDailyTokens(date)
	if err != nil {
		t.Fatalf("GetDailyTokens: %v", err)
	}
	if cost != tok.CostUSD {
		t.Errorf("cost mismatch: GetDailyCost=%f GetDailyTokens=%f", cost, tok.CostUSD)
	}
	if want := int64(110); tok.Total != want {
		t.Errorf("total: got %d want %d", tok.Total, want)
	}
}

func TestGetDailyTokensEmptyDay(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	got, err := m.GetDailyTokens("2000-01-01")
	if err != nil {
		t.Fatalf("GetDailyTokens: %v", err)
	}
	if got.Total != 0 || got.CostUSD != 0 {
		t.Errorf("empty day: got %+v want zeroes", got)
	}
	if got.Date != "2000-01-01" {
		t.Errorf("date not echoed: %q", got.Date)
	}
}

func TestGetProjectTokens(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	seedDaily(t, st, today, "lumen", 100, 10, 1000, 50, 0.10)
	seedDaily(t, st, yesterday, "lumen", 200, 20, 2000, 100, 0.20)
	// A different project must not leak into the total.
	seedDaily(t, st, today, "other", 999, 999, 999, 999, 9.99)

	got, err := m.GetProjectTokens("lumen", 7)
	if err != nil {
		t.Fatalf("GetProjectTokens: %v", err)
	}
	if want := int64(3480); got.Total != want {
		t.Errorf("total: got %d want %d", got.Total, want)
	}
	if got.CacheRead != 3000 {
		t.Errorf("cache read: got %d want 3000", got.CacheRead)
	}
	if got.CostUSD < 0.29 || got.CostUSD > 0.31 {
		t.Errorf("cost: got %f want ~0.30", got.CostUSD)
	}
}

func TestGetProjectTokensUnknownProject(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	got, err := m.GetProjectTokens("nope", 7)
	if err != nil {
		t.Fatalf("GetProjectTokens: %v", err)
	}
	if got.Total != 0 {
		t.Errorf("unknown project: got %+v want zeroes", got)
	}
}
