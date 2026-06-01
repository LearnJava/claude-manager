package session

import (
	"path/filepath"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/permission"
	"claude-manager/internal/store"
)

// newTestManager builds a SessionManager with no SQLite store, which is the
// common case for tests that exercise in-memory logic only. Tests that need
// the store should call newTestManagerWithStore (which skips if cgo is off).
func newTestManager(t *testing.T) *SessionManager {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.AppConfig{
		Settings: config.GlobalSettings{
			ClaudePath:        "claude",
			DefaultRetryDelay: 1,
			RateLimitPause:    1,
			SessionStartDelay: 0,
		},
		Projects: []config.ProjectConfig{
			{
				Name: "lumen",
				Path: dir,
				Sessions: []config.SessionConfig{
					{Name: "P1", Model: "sonnet", PermissionMode: "acceptEdits"},
					{Name: "P2", Model: "sonnet", PermissionMode: "bypassPermissions"},
				},
			},
		},
	}
	return NewSessionManager(cfg, filepath.Join(dir, "config.toml"), nil)
}

func newTestManagerWithStore(t *testing.T) (*SessionManager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		// CGO is required for go-sqlite3; skip rather than fail when off.
		t.Skipf("store unavailable: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := newTestManager(t)
	m.store = st
	return m, st
}

// addStubSession registers a managed Session without spawning a process so
// tests can exercise event handling, metrics aggregation, and state assembly
// in isolation.
func addStubSession(m *SessionManager, project, name string) *managedSession {
	sc := config.SessionConfig{Name: name, Model: "sonnet", PermissionMode: "acceptEdits"}
	sess := New(Params{
		ID:          sessionID(project, name),
		ProjectName: project,
		Config:      sc,
		OnEvent: func(id string, ev SessionEvent) {
			m.onSessionEvent(id, ev)
		},
	})
	ms := &managedSession{session: sess, project: project, name: name}
	m.mu.Lock()
	m.sessions[sess.ID] = ms
	m.mu.Unlock()
	return ms
}

func TestFindConfig(t *testing.T) {
	m := newTestManager(t)

	if _, _, err := m.findConfig("lumen", "P1"); err != nil {
		t.Errorf("findConfig lumen/P1: %v", err)
	}
	if _, _, err := m.findConfig("lumen", "missing"); err == nil {
		t.Error("expected error for missing session")
	}
	if _, _, err := m.findConfig("absent", "P1"); err == nil {
		t.Error("expected error for missing project")
	}
}

func TestSessionID(t *testing.T) {
	if got := sessionID("lumen", "P1"); got != "lumen/P1" {
		t.Errorf("sessionID = %q", got)
	}
}

func TestReserveStartSlotZeroDelay(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Settings.SessionStartDelay = 0

	if wait := m.reserveStartSlot(); wait != 0 {
		t.Errorf("expected zero wait with delay=0, got %v", wait)
	}
	if wait := m.reserveStartSlot(); wait != 0 {
		t.Errorf("expected zero wait with delay=0 (second call), got %v", wait)
	}
}

func TestReserveStartSlotStaggers(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Settings.SessionStartDelay = 2

	if wait := m.reserveStartSlot(); wait != 0 {
		t.Errorf("first slot should be immediate, got %v", wait)
	}
	wait2 := m.reserveStartSlot()
	if wait2 <= 0 || wait2 > 2*time.Second+50*time.Millisecond {
		t.Errorf("second slot should be ~2s, got %v", wait2)
	}
	wait3 := m.reserveStartSlot()
	if wait3 < wait2 {
		t.Errorf("third slot must be >= second; got %v < %v", wait3, wait2)
	}
}

func TestStopSessionMissing(t *testing.T) {
	m := newTestManager(t)
	if err := m.StopSession("nope/x", true); err == nil {
		t.Error("expected error stopping unknown session")
	}
}

func TestSendMessageMissing(t *testing.T) {
	m := newTestManager(t)
	if err := m.SendMessage("nope/x", "hi"); err == nil {
		t.Error("expected error sending message to unknown session")
	}
}

func TestRespondPermissionMissing(t *testing.T) {
	m := newTestManager(t)
	if err := m.RespondPermission("nope/x", "r1", "allow"); err == nil {
		t.Error("expected error responding for unknown session")
	}
}

func TestGetAllSessionsEmpty(t *testing.T) {
	m := newTestManager(t)
	if got := m.GetAllSessions(); len(got) != 0 {
		t.Errorf("expected empty slice, got %d entries", len(got))
	}
}

func TestGetSessionMissing(t *testing.T) {
	m := newTestManager(t)
	if _, ok := m.GetSession("nope/x"); ok {
		t.Error("expected GetSession to return false for unknown id")
	}
}

func TestGetSessionMetricsMissing(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.GetSessionMetrics("nope/x"); err == nil {
		t.Error("expected error for unknown session")
	}
}

func TestStopProjectNoSessions(t *testing.T) {
	m := newTestManager(t)
	if err := m.StopProject("missing"); err != nil {
		t.Errorf("StopProject for empty project should be a no-op, got %v", err)
	}
}

func TestStartProjectMissing(t *testing.T) {
	m := newTestManager(t)
	if err := m.StartProject("missing"); err == nil {
		t.Error("expected error starting unknown project")
	}
}

func TestGetSessionStateFromStub(t *testing.T) {
	m := newTestManager(t)
	addStubSession(m, "lumen", "P1")

	st, ok := m.GetSession("lumen/P1")
	if !ok {
		t.Fatal("expected stub session to be returned")
	}
	if st.Project != "lumen" || st.Name != "P1" {
		t.Errorf("unexpected state: %+v", st)
	}
	if st.Status != "idle" {
		t.Errorf("Status = %q, want idle", st.Status)
	}
	if st.Model != "sonnet" {
		t.Errorf("Model = %q", st.Model)
	}
}

func TestGetAllSessionsReturnsAll(t *testing.T) {
	m := newTestManager(t)
	addStubSession(m, "lumen", "P1")
	addStubSession(m, "lumen", "P2")

	all := m.GetAllSessions()
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(all))
	}
}

func TestHandleUsageAccumulates(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "P1")

	m.handleUsage(ms, &TokenUsage{InputTokens: 100, OutputTokens: 50, CacheReadInputTokens: 200})
	m.handleUsage(ms, &TokenUsage{InputTokens: 10, OutputTokens: 5, CacheCreationInputTokens: 30})

	metrics, err := m.GetSessionMetrics("lumen/P1")
	if err != nil {
		t.Fatalf("GetSessionMetrics: %v", err)
	}
	if metrics.InputTokens != 110 {
		t.Errorf("InputTokens = %d, want 110", metrics.InputTokens)
	}
	if metrics.OutputTokens != 55 {
		t.Errorf("OutputTokens = %d, want 55", metrics.OutputTokens)
	}
	if metrics.CacheRead != 200 {
		t.Errorf("CacheRead = %d, want 200", metrics.CacheRead)
	}
	if metrics.CacheCreation != 30 {
		t.Errorf("CacheCreation = %d, want 30", metrics.CacheCreation)
	}
	if metrics.NumTurns != 2 {
		t.Errorf("NumTurns = %d, want 2", metrics.NumTurns)
	}
}

func TestHandleResultCapturesContextWindow(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "P1")

	m.handleUsage(ms, &TokenUsage{InputTokens: 50000})
	m.handleResult(ms, &SessionResult{
		TotalCostUSD: 1.25,
		NumTurns:     7,
		DurationMs:   12345,
		ModelUsage: map[string]ModelUsage{
			"claude-sonnet-4-6": {ContextWindow: 200000},
		},
	})

	metrics, _ := m.GetSessionMetrics("lumen/P1")
	if metrics.TotalCostUSD != 1.25 {
		t.Errorf("TotalCostUSD = %v", metrics.TotalCostUSD)
	}
	if metrics.NumTurns != 7 {
		t.Errorf("NumTurns = %d", metrics.NumTurns)
	}
	if metrics.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d", metrics.ContextWindow)
	}
	if metrics.ContextUtil <= 0 || metrics.ContextUtil > 1 {
		t.Errorf("ContextUtil should be 0<util<=1, got %v", metrics.ContextUtil)
	}
}

func TestHandlePermissionAutoAllowsViaConfigRule(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "P1")
	ms.session.Config.PermissionRules = []config.PermissionRule{
		{Tool: "Bash", Pattern: "ls", Decision: "allow"},
	}

	// Permission request must not be queued if a rule auto-allows it.
	// session.RespondPermission will return an error because no run is in
	// flight; that's fine — we only care that the rule path was taken
	// (i.e. the request never landed on the queue).
	m.handlePermission(ms, &PermissionRequest{
		ID:      "r1",
		Tool:    "Bash",
		Command: "ls",
	})
	if m.queue.Len() != 0 {
		t.Errorf("config rule must auto-resolve; queue len = %d", m.queue.Len())
	}
}

func TestHandlePermissionBypassMode(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "bypass")
	ms.session.Config.PermissionMode = "bypassPermissions"

	m.handlePermission(ms, &PermissionRequest{ID: "r2", Tool: "Bash", Command: "anything"})
	if m.queue.Len() != 0 {
		t.Errorf("bypassPermissions must auto-allow; queue len = %d", m.queue.Len())
	}
}

func TestHandlePermissionQueuesWhenNoRuleMatches(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "P1")

	m.handlePermission(ms, &PermissionRequest{
		ID:      "r3",
		Tool:    "Bash",
		Command: "rm -rf /",
	})
	if m.queue.Len() != 1 {
		t.Fatalf("expected 1 queued request, got %d", m.queue.Len())
	}
	pend := m.GetPendingPermissions()
	if len(pend) != 1 || pend[0].ID != "r3" {
		t.Errorf("unexpected queue contents: %+v", pend)
	}
	if pend[0].SessionID != "lumen/P1" {
		t.Errorf("SessionID = %q, want lumen/P1", pend[0].SessionID)
	}
}

func TestRespondPermissionRegistersRuntimeRule(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "P1")

	// Queue a request manually so we don't depend on session internals.
	req := permission.PermissionRequest{
		ID: "r4", SessionID: ms.session.ID, Tool: "Bash", Command: "npm install",
	}
	m.queue.Add(req)

	// allow_session should add a runtime rule but session.RespondPermission
	// will fail because no run is in flight. We only assert the rule path.
	_ = m.RespondPermission(ms.session.ID, "r4", "allow_session")

	if m.queue.Len() != 0 {
		t.Errorf("queue should be drained after response; got len=%d", m.queue.Len())
	}
	if !m.runtimeRules.Allows(req) {
		t.Error("runtime rule for the request should now exist")
	}
}

func TestGetRateLimitStatusEmpty(t *testing.T) {
	m := newTestManager(t)
	if got := m.GetRateLimitStatus(); got != nil {
		t.Errorf("expected nil rate limit, got %+v", got)
	}
}

func TestGetRateLimitStatusReturnsLatest(t *testing.T) {
	m := newTestManager(t)
	a := addStubSession(m, "lumen", "P1")
	b := addStubSession(m, "lumen", "P2")

	a.rateLimit = &RateLimitInfo{Status: "ok"}
	a.rateLimitUntil = time.Now().Add(10 * time.Minute)
	b.rateLimit = &RateLimitInfo{Status: "later"}
	b.rateLimitUntil = time.Now().Add(30 * time.Minute)

	got := m.GetRateLimitStatus()
	if got == nil || got.Status != "later" {
		t.Errorf("expected latest reset (P2), got %+v", got)
	}
}

func TestGetRateLimitStatusIgnoresExpired(t *testing.T) {
	m := newTestManager(t)
	a := addStubSession(m, "lumen", "P1")
	a.rateLimit = &RateLimitInfo{Status: "old"}
	a.rateLimitUntil = time.Now().Add(-1 * time.Minute) // already expired

	if got := m.GetRateLimitStatus(); got != nil {
		t.Errorf("expected nil for expired limit, got %+v", got)
	}
}

func TestGetDailyCostSumsAcrossProjects(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	today := time.Now().Format("2006-01-02")

	if err := st.AddDailyMetrics(&store.DailyMetrics{Date: today, Project: "lumen", TotalCost: 1.5}); err != nil {
		t.Fatalf("AddDailyMetrics: %v", err)
	}
	// Project "lumen" is configured; another project not in config should not count.
	if err := st.AddDailyMetrics(&store.DailyMetrics{Date: today, Project: "other", TotalCost: 99}); err != nil {
		t.Fatalf("AddDailyMetrics: %v", err)
	}

	got, err := m.GetDailyCost(today)
	if err != nil {
		t.Fatalf("GetDailyCost: %v", err)
	}
	if got != 1.5 {
		t.Errorf("GetDailyCost = %v, want 1.5", got)
	}
}

func TestGetProjectCost(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	today := time.Now().Format("2006-01-02")
	if err := st.AddDailyMetrics(&store.DailyMetrics{Date: today, Project: "lumen", TotalCost: 2.5}); err != nil {
		t.Fatalf("AddDailyMetrics: %v", err)
	}

	got, err := m.GetProjectCost("lumen", 30)
	if err != nil {
		t.Fatalf("GetProjectCost: %v", err)
	}
	if got != 2.5 {
		t.Errorf("GetProjectCost = %v, want 2.5", got)
	}
}

func TestSetConfigReplacesConfig(t *testing.T) {
	m := newTestManager(t)
	newCfg := &config.AppConfig{
		Settings: config.GlobalSettings{ClaudePath: "/usr/bin/claude"},
	}
	m.SetConfig(newCfg)
	if m.cfg.Settings.ClaudePath != "/usr/bin/claude" {
		t.Errorf("SetConfig did not replace config")
	}
}

func TestEmitNoCtxIsNoOp(t *testing.T) {
	m := newTestManager(t)
	// No ctx set; emit must not panic.
	m.emit(EventNameStatus, StatusEvent{ID: "x", Status: "idle"})
}

func TestClearSessionState_NoOp(t *testing.T) {
	m := newTestManager(t)
	// Must not panic even if no state file exists.
	m.ClearSessionState("lumen", "P1")
}

func TestGetSessionState_NilWhenAbsent(t *testing.T) {
	m := newTestManager(t)
	if got := m.GetSessionState("lumen", "P1"); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestClearSessionState_RemovesExistingState(t *testing.T) {
	m := newTestManager(t)

	// Manually save a state via the stateStore.
	if err := m.stateStore.Save("lumen", "P1", &PersistedState{SessionID: "test-sid"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// GetSessionState should find it.
	if got := m.GetSessionState("lumen", "P1"); got == nil || got.SessionID != "test-sid" {
		t.Fatalf("expected saved state, got %v", got)
	}

	// Clear should remove it.
	m.ClearSessionState("lumen", "P1")
	if got := m.GetSessionState("lumen", "P1"); got != nil {
		t.Errorf("expected nil after ClearSessionState, got %+v", got)
	}
}

func TestManagerStateStoreDir(t *testing.T) {
	m := newTestManager(t)
	if m.stateStore == nil {
		t.Fatal("stateStore must be initialised by NewSessionManager")
	}
}
