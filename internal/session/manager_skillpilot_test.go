package session

import (
	"errors"
	"testing"
)

// TestRunSkillAutopilot_OnlyForOptedInProject: the autopilot is wired
// app-wide but must only run for a project with AutoSkills on, and a change
// it reports must reach the frontend as skills:changed (LEARN-TASKS.md
// LN-27).
func TestRunSkillAutopilot_OnlyForOptedInProject(t *testing.T) {
	m := newTestManager(t)
	em := &captureEmitter{}
	m.emitter = em

	var calls int
	var gotBudget float64
	m.SetSkillAutopilot(func(project, projectPath string, gates []string, budget float64) (bool, error) {
		calls++
		gotBudget = budget
		return true, nil
	})

	m.runSkillAutopilot("lumen", "/tmp/lumen")
	if calls != 0 {
		t.Fatalf("autopilot ran %d times for a project with AutoSkills off", calls)
	}

	m.cfg.Projects[0].AutoSkills = true
	m.cfg.Projects[0].AutoSkillsDailyBudgetUSD = 1.5
	m.runSkillAutopilot("lumen", "/tmp/lumen")
	if calls != 1 || gotBudget != 1.5 {
		t.Fatalf("calls = %d, budget = %v; want 1 call with budget 1.5", calls, gotBudget)
	}
	var changed *SkillsChangedEvent
	for _, e := range em.events {
		if e.Name == EventNameSkillsChanged {
			ev := e.Data.(SkillsChangedEvent)
			changed = &ev
		}
	}
	if changed == nil || changed.Project != "lumen" {
		t.Fatalf("skills:changed not emitted for lumen: %+v", em.events)
	}
}

// TestRunSkillAutopilot_ErrorIsLoggedNotEmitted: a failing tick that
// changed nothing must not trigger a pointless frontend reload.
func TestRunSkillAutopilot_ErrorIsLoggedNotEmitted(t *testing.T) {
	m := newTestManager(t)
	em := &captureEmitter{}
	m.emitter = em
	m.cfg.Projects[0].AutoSkills = true
	m.SetSkillAutopilot(func(string, string, []string, float64) (bool, error) {
		return false, errors.New("boom")
	})
	m.runSkillAutopilot("lumen", "/tmp/lumen")
	for _, e := range em.events {
		if e.Name == EventNameSkillsChanged {
			t.Fatal("skills:changed emitted for a tick that changed nothing")
		}
	}
}
