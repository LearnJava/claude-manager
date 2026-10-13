package session

import (
	"fmt"
	"time"

	"claude-manager/internal/logger"
)

// Budget alerts (PLAN.md §18.6): after every finished run the manager sums
// the spend of today and of the last 7 days across all projects and, when
// daily_budget_alert / weekly_budget_alert is crossed, emits alert:budget
// once per day / per week. The frontend shows it as a toast. Thresholds are
// read from the live config, so a change in the UI applies to the next run.

// EventNameBudgetAlert is emitted when a spending threshold is crossed.
const EventNameBudgetAlert = "alert:budget"

// BudgetAlert is the alert:budget payload.
type BudgetAlert struct {
	Kind    string  `json:"kind"` // "daily" | "weekly"
	Current float64 `json:"current"`
	Limit   float64 `json:"limit"`
}

// budgetAlertState remembers which day/week was already alerted so every
// later run past the threshold does not alert again.
type budgetAlertState struct {
	lastDaily  string // "2006-01-02"
	lastWeekly string // "2006-W01"
}

// checkBudgetAlerts compares today's and the last 7 days' spend (local
// dates, matching how finishRun writes daily_metrics) with the thresholds.
func (m *SessionManager) checkBudgetAlerts(now time.Time) {
	if m.store == nil {
		return
	}
	m.mu.Lock()
	daily := m.cfg.Settings.DailyBudgetAlert
	weekly := m.cfg.Settings.WeeklyBudgetAlert
	projects := make([]string, 0, len(m.cfg.Projects))
	for _, p := range m.cfg.Projects {
		projects = append(projects, p.Name)
	}
	m.mu.Unlock()
	if daily <= 0 && weekly <= 0 {
		return
	}

	dayCost := func(date string) float64 {
		var total float64
		for _, p := range projects {
			dm, err := m.store.GetDailyMetrics(date, p)
			if err != nil {
				logger.L.Warn("budget_alert.metrics_failed", "date", date, "project", p, "err", err)
				continue
			}
			if dm != nil {
				total += dm.TotalCost
			}
		}
		return total
	}

	today := now.Format("2006-01-02")
	todayCost := dayCost(today)
	if daily > 0 && todayCost >= daily && m.markBudgetAlert(&m.budgetAlerts.lastDaily, today) {
		m.emit(EventNameBudgetAlert, BudgetAlert{Kind: "daily", Current: todayCost, Limit: daily})
	}

	if weekly > 0 {
		weekCost := todayCost
		for i := 1; i < 7; i++ {
			weekCost += dayCost(now.AddDate(0, 0, -i).Format("2006-01-02"))
		}
		year, week := now.ISOWeek()
		key := fmt.Sprintf("%d-W%02d", year, week)
		if weekCost >= weekly && m.markBudgetAlert(&m.budgetAlerts.lastWeekly, key) {
			m.emit(EventNameBudgetAlert, BudgetAlert{Kind: "weekly", Current: weekCost, Limit: weekly})
		}
	}
}

// markBudgetAlert records key in *slot and reports whether it was new.
func (m *SessionManager) markBudgetAlert(slot *string, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if *slot == key {
		return false
	}
	*slot = key
	return true
}
