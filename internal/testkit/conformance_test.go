package testkit_test

import (
	"testing"

	"claude-manager/internal/session"
	"claude-manager/internal/testkit"
)

const scenariosPath = "../../testdata/scenarios"

// TestConformance_AllEventsRecognized verifies that every emit event in every
// scenario file is parsed to a known event type by the session parser.
// This is the core regression guard: fakeclaude output must stay compatible
// with parser.go. Any new event format added to a scenario must be parseable.
func TestConformance_AllEventsRecognized(t *testing.T) {
	scenarios, err := testkit.LoadScenariosDir(scenariosPath)
	if err != nil {
		t.Fatalf("LoadScenariosDir: %v", err)
	}
	if len(scenarios) == 0 {
		t.Fatalf("no scenarios found in %s", scenariosPath)
	}

	for _, s := range scenarios {
		s := s
		t.Run(s.Name, func(t *testing.T) {
			for i, step := range s.Steps {
				if step.Type != "emit" || len(step.Event) == 0 {
					continue
				}
				line := string(step.Event)
				evt := session.ParseLine(line)
				if evt.EventType == session.EventUnknown {
					t.Errorf("step[%d]: parsed as EventUnknown\n  event: %s", i, line)
					continue
				}
				assertEventFieldsPopulated(t, i, evt)
			}
		})
	}
}

// assertEventFieldsPopulated checks that the type-specific payload pointer is non-nil
// for every named event type.
func assertEventFieldsPopulated(t *testing.T, stepIdx int, evt session.ParsedEvent) {
	t.Helper()
	switch evt.EventType {
	case session.EventInit:
		if evt.Init == nil {
			t.Errorf("step[%d]: EventInit but Init is nil", stepIdx)
		}
	case session.EventResult:
		if evt.Result == nil {
			t.Errorf("step[%d]: EventResult but Result is nil", stepIdx)
		}
	case session.EventRateLimit:
		if evt.RateLimit == nil {
			t.Errorf("step[%d]: EventRateLimit but RateLimit is nil", stepIdx)
		}
	case session.EventPermission:
		if evt.Permission == nil {
			t.Errorf("step[%d]: EventPermission but Permission is nil", stepIdx)
		}
	}
}

// --- Per-type field population tests using the simplest scenario that exercises each type ---

// TestConformance_InitInfo verifies that system/init events populate InitInfo correctly.
func TestConformance_InitInfo(t *testing.T) {
	s, err := testkit.LoadScenario(scenariosPath + "/happy-path.json")
	if err != nil {
		t.Fatalf("load happy-path: %v", err)
	}

	for _, step := range s.Steps {
		if step.Type != "emit" {
			continue
		}
		evt := session.ParseLine(string(step.Event))
		if evt.EventType != session.EventInit {
			continue
		}
		if evt.Init == nil {
			t.Fatal("Init is nil for system/init event")
		}
		// Without var substitution the placeholder "${model}" is a non-empty string.
		if evt.Init.Model == "" {
			t.Error("Init.Model is empty")
		}
		if len(evt.Init.Tools) == 0 {
			t.Error("Init.Tools is empty")
		}
		return
	}
	t.Fatal("no system/init event found in happy-path scenario")
}

// TestConformance_TokenUsage verifies that assistant events populate TokenUsage.
func TestConformance_TokenUsage(t *testing.T) {
	s, err := testkit.LoadScenario(scenariosPath + "/happy-path.json")
	if err != nil {
		t.Fatalf("load happy-path: %v", err)
	}

	for _, step := range s.Steps {
		if step.Type != "emit" {
			continue
		}
		evt := session.ParseLine(string(step.Event))
		if evt.EventType != session.EventLog || evt.Usage == nil {
			continue
		}
		if evt.Usage.InputTokens <= 0 && evt.Usage.OutputTokens <= 0 {
			t.Error("TokenUsage has no non-zero token counts")
		}
		return
	}
	t.Fatal("no assistant event with usage found in happy-path scenario")
}

// TestConformance_SessionResult verifies that result events populate SessionResult.
func TestConformance_SessionResult(t *testing.T) {
	s, err := testkit.LoadScenario(scenariosPath + "/happy-path.json")
	if err != nil {
		t.Fatalf("load happy-path: %v", err)
	}

	for _, step := range s.Steps {
		if step.Type != "emit" {
			continue
		}
		evt := session.ParseLine(string(step.Event))
		if evt.EventType != session.EventResult {
			continue
		}
		if evt.Result == nil {
			t.Fatal("Result is nil for result event")
		}
		if evt.Result.TotalCostUSD <= 0 {
			t.Errorf("TotalCostUSD = %v, want > 0", evt.Result.TotalCostUSD)
		}
		if evt.Result.NumTurns <= 0 {
			t.Errorf("NumTurns = %d, want > 0", evt.Result.NumTurns)
		}
		return
	}
	t.Fatal("no result event found in happy-path scenario")
}

// TestConformance_PermissionRequest verifies that permission_request events
// populate PermissionRequest with Tool and ID.
func TestConformance_PermissionRequest(t *testing.T) {
	s, err := testkit.LoadScenario(scenariosPath + "/permission-allow.json")
	if err != nil {
		t.Fatalf("load permission-allow: %v", err)
	}

	for _, step := range s.Steps {
		if step.Type != "emit" {
			continue
		}
		evt := session.ParseLine(string(step.Event))
		if evt.EventType != session.EventPermission {
			continue
		}
		if evt.Permission == nil {
			t.Fatal("Permission is nil for permission_request event")
		}
		if evt.Permission.Tool == "" {
			t.Error("Permission.Tool is empty")
		}
		if evt.Permission.ID == "" {
			t.Error("Permission.ID is empty")
		}
		return
	}
	t.Fatal("no permission_request event found in permission-allow scenario")
}

// --- Scenario-specific conformance tests for the new scenarios ---

// TestConformance_RateLimit verifies the rate-limit scenario emits a parseable
// rate_limit_event with utilization 0.9 and the expected resetsAt timestamp.
func TestConformance_RateLimit(t *testing.T) {
	s, err := testkit.LoadScenario(scenariosPath + "/rate-limit.json")
	if err != nil {
		t.Fatalf("load rate-limit: %v", err)
	}

	var found *session.RateLimitInfo
	for _, step := range s.Steps {
		if step.Type != "emit" {
			continue
		}
		evt := session.ParseLine(string(step.Event))
		if evt.EventType == session.EventRateLimit && evt.RateLimit != nil {
			found = evt.RateLimit
			break
		}
	}

	if found == nil {
		t.Fatal("no rate_limit_event found in rate-limit scenario")
	}
	if found.Utilization != 0.9 {
		t.Errorf("Utilization = %v, want 0.9", found.Utilization)
	}
	if found.ResetsAt != 1779584400 {
		t.Errorf("ResetsAt = %d, want 1779584400", found.ResetsAt)
	}
	if found.Status != "allowed_warning" {
		t.Errorf("Status = %q, want allowed_warning", found.Status)
	}
	if found.SurpassedThreshold != 0.75 {
		t.Errorf("SurpassedThreshold = %v, want 0.75", found.SurpassedThreshold)
	}
}

// TestConformance_Loop verifies the loop scenario emits at least 3 identical
// Bash tool_use entries — the minimum for LoopDetector to fire.
func TestConformance_Loop(t *testing.T) {
	s, err := testkit.LoadScenario(scenariosPath + "/loop.json")
	if err != nil {
		t.Fatalf("load loop: %v", err)
	}

	var toolInputs []string
	for _, step := range s.Steps {
		if step.Type != "emit" {
			continue
		}
		evt := session.ParseLine(string(step.Event))
		if evt.EventType != session.EventLog {
			continue
		}
		for _, e := range evt.Entries {
			if e.ToolName == "Bash" {
				toolInputs = append(toolInputs, e.ToolInput)
			}
		}
	}

	if len(toolInputs) < 3 {
		t.Fatalf("expected >= 3 Bash tool_use entries for loop detection, got %d", len(toolInputs))
	}
	for i := 1; i < len(toolInputs); i++ {
		if toolInputs[i] != toolInputs[0] {
			t.Errorf("toolInput[%d] = %q, want identical to toolInput[0] = %q", i, toolInputs[i], toolInputs[0])
		}
	}
}

// TestConformance_ContextGrowth verifies the context-growth scenario emits an
// assistant event whose input_tokens exceed 75% of the scenario's context_window
// (triggering the auto-restart monitor) and that the result reports the full
// contextWindow in modelUsage.
func TestConformance_ContextGrowth(t *testing.T) {
	s, err := testkit.LoadScenario(scenariosPath + "/context-growth.json")
	if err != nil {
		t.Fatalf("load context-growth: %v", err)
	}

	const contextWindow = 200000
	const threshold = 150000 // 75% of contextWindow

	var maxInputTokens int
	var resultContextWindow int
	for _, step := range s.Steps {
		if step.Type != "emit" {
			continue
		}
		evt := session.ParseLine(string(step.Event))
		if evt.EventType == session.EventLog && evt.Usage != nil {
			if evt.Usage.InputTokens > maxInputTokens {
				maxInputTokens = evt.Usage.InputTokens
			}
		}
		if evt.EventType == session.EventResult && evt.Result != nil {
			if mu, ok := evt.Result.ModelUsage["claude-sonnet-4-6"]; ok {
				resultContextWindow = mu.ContextWindow
			}
		}
	}

	if maxInputTokens <= threshold {
		t.Errorf("max input_tokens across turns = %d, want > %d (75%% of context_window %d)", maxInputTokens, threshold, contextWindow)
	}
	if resultContextWindow != contextWindow {
		t.Errorf("result modelUsage contextWindow = %d, want %d", resultContextWindow, contextWindow)
	}
}

// TestConformance_MultiTurn verifies the multi-turn scenario interleaves at
// least 3 await_stdin steps with 3 assistant responses and 1 result.
func TestConformance_MultiTurn(t *testing.T) {
	s, err := testkit.LoadScenario(scenariosPath + "/multi-turn.json")
	if err != nil {
		t.Fatalf("load multi-turn: %v", err)
	}

	var awaitCount, initCount, assistantCount, resultCount int
	for _, step := range s.Steps {
		if step.Type == "await_stdin" {
			awaitCount++
			continue
		}
		if step.Type != "emit" {
			continue
		}
		evt := session.ParseLine(string(step.Event))
		switch evt.EventType {
		case session.EventInit:
			initCount++
		case session.EventLog:
			assistantCount++
		case session.EventResult:
			resultCount++
		}
	}

	if awaitCount < 3 {
		t.Errorf("await_stdin count = %d, want >= 3 (bidirectional multi-turn)", awaitCount)
	}
	if assistantCount < 3 {
		t.Errorf("assistant message count = %d, want >= 3", assistantCount)
	}
	if initCount != 1 {
		t.Errorf("init count = %d, want 1", initCount)
	}
	if resultCount != 1 {
		t.Errorf("result count = %d, want 1", resultCount)
	}
}

// TestConformance_BudgetExceeded verifies the budget-exceeded scenario emits a
// result whose total_cost_usd is well above a representative max_budget_usd of 0.50.
func TestConformance_BudgetExceeded(t *testing.T) {
	s, err := testkit.LoadScenario(scenariosPath + "/budget-exceeded.json")
	if err != nil {
		t.Fatalf("load budget-exceeded: %v", err)
	}

	const representativeMaxBudget = 0.50

	var totalCost float64
	for _, step := range s.Steps {
		if step.Type != "emit" {
			continue
		}
		evt := session.ParseLine(string(step.Event))
		if evt.EventType == session.EventResult && evt.Result != nil {
			if evt.Result.TotalCostUSD > totalCost {
				totalCost = evt.Result.TotalCostUSD
			}
		}
	}

	if totalCost <= representativeMaxBudget {
		t.Errorf("TotalCostUSD = %v, want > %v (budget-exceeded scenario must overshoot max_budget_usd)", totalCost, representativeMaxBudget)
	}
}

// TestConformance_AllScenariosPresent ensures the full required scenario set
// from PLAN.md §21.3.3 is present in the testdata directory.
func TestConformance_AllScenariosPresent(t *testing.T) {
	required := []string{
		"happy-path",
		"permission-allow",
		"permission-deny",
		"error-exit",
		"rate-limit",
		"loop",
		"context-growth",
		"multi-turn",
		"budget-exceeded",
	}

	scenarios, err := testkit.LoadScenariosDir(scenariosPath)
	if err != nil {
		t.Fatalf("LoadScenariosDir: %v", err)
	}

	names := make(map[string]bool, len(scenarios))
	for _, s := range scenarios {
		names[s.Name] = true
	}

	for _, name := range required {
		if !names[name] {
			t.Errorf("required scenario %q not found in %s", name, scenariosPath)
		}
	}
}
