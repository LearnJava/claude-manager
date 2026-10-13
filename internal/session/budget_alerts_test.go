package session

import (
	"testing"
	"time"

	"claude-manager/internal/store"
)

func budgetAlerts(em *captureEmitter) []BudgetAlert {
	var out []BudgetAlert
	for _, e := range em.events {
		if e.Name == EventNameBudgetAlert {
			out = append(out, e.Data.(BudgetAlert))
		}
	}
	return out
}

func TestCheckBudgetAlerts(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	em := &captureEmitter{}
	m.emitter = em
	now := time.Date(2026, 10, 13, 15, 0, 0, 0, time.Local)

	addCost := func(daysAgo int, cost float64) {
		if err := st.AddDailyMetrics(&store.DailyMetrics{
			Date: now.AddDate(0, 0, -daysAgo).Format("2006-01-02"), Project: "lumen", TotalCost: cost, TotalRuns: 1,
		}); err != nil {
			t.Fatalf("AddDailyMetrics: %v", err)
		}
	}
	addCost(0, 4)  // today
	addCost(3, 10) // inside the 7-day window
	addCost(9, 50) // outside it

	// Thresholds off → no alerts at all.
	m.checkBudgetAlerts(now)
	if got := budgetAlerts(em); len(got) != 0 {
		t.Fatalf("alerts with thresholds off: %+v", got)
	}

	// Below both thresholds.
	m.cfg.Settings.DailyBudgetAlert = 5
	m.cfg.Settings.WeeklyBudgetAlert = 20
	m.checkBudgetAlerts(now)
	if got := budgetAlerts(em); len(got) != 0 {
		t.Fatalf("alerts below thresholds: %+v", got)
	}

	// Cross both: one daily + one weekly alert.
	addCost(0, 7) // today = 11, week = 21
	m.checkBudgetAlerts(now)
	got := budgetAlerts(em)
	if len(got) != 2 || got[0].Kind != "daily" || got[0].Current != 11 || got[1].Kind != "weekly" || got[1].Current != 21 {
		t.Fatalf("unexpected alerts: %+v", got)
	}

	// Same day / week again → deduplicated.
	addCost(0, 1)
	m.checkBudgetAlerts(now)
	if got := budgetAlerts(em); len(got) != 2 {
		t.Fatalf("alerts not deduplicated: %+v", got)
	}

	// Next day: the daily alert fires again, the weekly one waits for a new week.
	m.checkBudgetAlerts(now.AddDate(0, 0, 1))
	got = budgetAlerts(em)
	if len(got) != 2 {
		// next day has no spend of its own: 0 < 5, so no new daily alert
		t.Fatalf("unexpected alerts on a day with no spend: %+v", got)
	}
}
