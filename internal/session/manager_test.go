package session

import (
	"path/filepath"
	"strings"
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
	return NewSessionManager(cfg, filepath.Join(dir, "config.toml"), nil, nil)
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
	var projectPath string
	for i := range m.cfg.Projects {
		if m.cfg.Projects[i].Name == project {
			projectPath = m.cfg.Projects[i].Path
			break
		}
	}
	sess := New(Params{
		ID:          sessionID(project, name),
		ProjectName: project,
		ProjectPath: projectPath,
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

func TestSetSessionModelMissing(t *testing.T) {
	m := newTestManager(t)
	if err := m.SetSessionModel("nope/x", "opus"); err == nil {
		t.Error("expected error setting model for unknown session")
	}
}

// TestSetSessionModel_NeverStartedUpdatesConfig verifies a session that is
// configured but was never started in this app process (no managedSession
// yet — the sidebar shows it via GetAllSessions' "configured" stub) still
// lets the model be switched: there's nothing running, so it just updates
// the in-memory config default for the next start.
func TestSetSessionModel_NeverStartedUpdatesConfig(t *testing.T) {
	m := newTestManager(t)
	if err := m.SetSessionModel("lumen/P1", "opus"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	_, sc, err := m.findConfig("lumen", "P1")
	if err != nil {
		t.Fatalf("findConfig: %v", err)
	}
	if sc.Model != "opus" {
		t.Errorf("Model = %q, want opus", sc.Model)
	}
}

func TestSetSessionModelEmptyRejected(t *testing.T) {
	m := newTestManager(t)
	addStubSession(m, "lumen", "P1")
	if err := m.SetSessionModel("lumen/P1", "  "); err == nil {
		t.Error("expected error for a blank model")
	}
}

// TestSetSessionModel_Autonomous verifies an autonomous session's model
// switch never restarts it: the running task keeps going, only the next
// task's launch (via Session.ActiveModel) sees the new value.
func TestSetSessionModel_Autonomous(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "P1")
	ms.session.Config.AutoRestart = true
	ms.session.setStatus(config.StatusWorking)

	if err := m.SetSessionModel("lumen/P1", "opus"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if got := ms.session.ActiveModel(); got != "opus" {
		t.Errorf("ActiveModel = %q, want opus", got)
	}
	// No restart attempted: status is untouched by SetSessionModel itself.
	if got := ms.session.Status(); got != config.StatusWorking {
		t.Errorf("status = %s, want it to stay working (no restart for an autonomous session)", got)
	}
}

// TestSetSessionModel_InteractiveIdle verifies an interactive session with
// nothing currently running just updates the model — there is no live
// process to restart.
func TestSetSessionModel_InteractiveIdle(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "P1")

	if err := m.SetSessionModel("lumen/P1", "opus"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if got := ms.session.ActiveModel(); got != "opus" {
		t.Errorf("ActiveModel = %q, want opus", got)
	}
	if got := ms.session.Status(); got != config.StatusIdle {
		t.Errorf("status = %s, want idle", got)
	}
}

// TestGetAllSessionsIncludesNeverStartedConfigured verifies that a session
// present in config but never launched (no managedSession entry yet) still
// shows up as an idle stub — otherwise selecting it in the UI before its
// first run renders nothing at all.
func TestGetAllSessionsIncludesNeverStartedConfigured(t *testing.T) {
	m := newTestManager(t)
	all := m.GetAllSessions()
	if len(all) != 2 {
		t.Fatalf("expected 2 configured-but-idle sessions, got %d", len(all))
	}
	byID := map[string]SessionState{}
	for _, s := range all {
		byID[s.ID] = s
	}
	p1, ok := byID["lumen/P1"]
	if !ok {
		t.Fatal("expected lumen/P1 in GetAllSessions")
	}
	if p1.Status != "idle" {
		t.Errorf("Status = %q, want idle", p1.Status)
	}
	if p1.Model != "sonnet" {
		t.Errorf("Model = %q, want sonnet", p1.Model)
	}
	if p1.PermissionMode != "acceptEdits" {
		t.Errorf("PermissionMode = %q, want acceptEdits", p1.PermissionMode)
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

// TestRecordPermissionEvent_GatedOnExperienceTracking (LEARN-TASKS.md LN-04):
// with the flag off (the default), handlePermission's auto-allow path must
// not touch the store at all — same "off means nothing is read/written"
// contract as the transcript indexer (LN-03).
func TestRecordPermissionEvent_GatedOnExperienceTracking(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	ms := addStubSession(m, "lumen", "P1")
	ms.session.Config.PermissionRules = []config.PermissionRule{
		{Tool: "Bash", Pattern: "ls", Decision: "allow"},
	}
	// ExperienceTracking left at its zero value (false).

	m.handlePermission(ms, &PermissionRequest{ID: "r1", Tool: "Bash", Command: "ls"})

	rows, err := st.PermissionEventsForProject("lumen")
	if err != nil {
		t.Fatalf("PermissionEventsForProject: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no permission_events rows with experience_tracking off, got %+v", rows)
	}
}

// TestHandlePermission_RecordsAutoDecision: with the flag on, a config-rule
// auto-allow is recorded with Auto=true.
func TestHandlePermission_RecordsAutoDecision(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	m.cfg.Optimization.ExperienceTracking = true
	ms := addStubSession(m, "lumen", "P1")
	ms.session.Config.PermissionRules = []config.PermissionRule{
		{Tool: "Bash", Pattern: "ls", Decision: "allow"},
	}

	m.handlePermission(ms, &PermissionRequest{ID: "r1", Tool: "Bash", Command: "ls"})

	rows, err := st.PermissionEventsForProject("lumen")
	if err != nil {
		t.Fatalf("PermissionEventsForProject: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !rows[0].Auto || rows[0].Decision != "allow" || rows[0].Tool != "Bash" || rows[0].Pattern != "ls" {
		t.Errorf("unexpected row: %+v", rows[0])
	}
}

// TestRespondPermission_RecordsHumanDecision: a human decision through
// RespondPermission is recorded with Auto=false.
func TestRespondPermission_RecordsHumanDecision(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	m.cfg.Optimization.ExperienceTracking = true
	ms := addStubSession(m, "lumen", "P1")

	req := permission.PermissionRequest{
		ID: "r5", SessionID: ms.session.ID, Tool: "Bash", Command: "npm test",
	}
	m.queue.Add(req)
	_ = m.RespondPermission(ms.session.ID, "r5", "allow")

	rows, err := st.PermissionEventsForProject("lumen")
	if err != nil {
		t.Fatalf("PermissionEventsForProject: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Auto || rows[0].Decision != "allow" || rows[0].Pattern != "npm test" {
		t.Errorf("unexpected row: %+v", rows[0])
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

func TestFinishRun_AutoSavesLogFile(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	ms := addStubSession(m, "lumen", "P1")

	m.beginRun(ms)
	ms.mu.Lock()
	ms.pendingLogs = append(ms.pendingLogs, store.LogEntry{Timestamp: time.Now(), Level: "text", Message: "hi"})
	ms.mu.Unlock()

	m.finishRun(ms, "completed", "")

	deadline := time.Now().Add(2 * time.Second)
	var files []store.LogFileInfo
	for time.Now().Before(deadline) {
		var err error
		files, err = store.ListProjectLogFiles(ms.session.ProjectPath)
		if err != nil {
			t.Fatalf("ListProjectLogFiles: %v", err)
		}
		if len(files) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 auto-saved log file, got %d", len(files))
	}
}

func TestFinishRun_NoLogsNoFile(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	ms := addStubSession(m, "lumen", "P1")

	m.beginRun(ms)
	m.finishRun(ms, "completed", "")

	files, err := store.ListProjectLogFiles(ms.session.ProjectPath)
	if err != nil {
		t.Fatalf("ListProjectLogFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no auto-saved file when there were no logs, got %d", len(files))
	}
}

// indexCall captures one ActionIndexFunc invocation for the tests below.
type indexCall struct {
	project, sessionName, cliSessionID, projectPath, taskPtr string
	runID                                                    int64
}

// TestFinishRun_IndexesActionsWhenTrackingEnabled: with experience_tracking
// on and an indexer wired (app.go's job normally), a completed run with a
// CLI session id triggers exactly one indexing call carrying the run's
// identity (LEARN-TASKS.md LN-03).
func TestFinishRun_IndexesActionsWhenTrackingEnabled(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	ms := addStubSession(m, "lumen", "P1")
	ms.session.CLISessionID = "cli-123"

	m.cfg.Optimization.ExperienceTracking = true
	calls := make(chan indexCall, 1)
	m.SetActionIndexer(func(project, sessionName string, runID int64, cliSessionID, projectPath, taskPtr string) error {
		calls <- indexCall{project, sessionName, cliSessionID, projectPath, taskPtr, runID}
		return nil
	})

	m.beginRun(ms)
	m.finishRun(ms, "completed", "")

	select {
	case c := <-calls:
		if c.project != "lumen" || c.sessionName != "P1" || c.cliSessionID != "cli-123" {
			t.Errorf("unexpected index call: %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the indexer to be called")
	}
}

// TestFinishRun_NoIndexingWhenTrackingDisabled: the flag being off (the
// default) must mean the indexer is never invoked, even though it's wired —
// "no transcript is opened at all", not just "the result is discarded".
func TestFinishRun_NoIndexingWhenTrackingDisabled(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	ms := addStubSession(m, "lumen", "P1")
	ms.session.CLISessionID = "cli-123"

	calls := make(chan indexCall, 1)
	m.SetActionIndexer(func(project, sessionName string, runID int64, cliSessionID, projectPath, taskPtr string) error {
		calls <- indexCall{project, sessionName, cliSessionID, projectPath, taskPtr, runID}
		return nil
	})

	m.beginRun(ms)
	m.finishRun(ms, "completed", "")

	select {
	case c := <-calls:
		t.Fatalf("indexer must not run with experience_tracking off, got %+v", c)
	case <-time.After(100 * time.Millisecond):
		// expected: nothing arrived
	}
}

// TestFinishRun_NoIndexerWiredIsSafe: the default (no app.go wiring at all)
// must not panic finishRun.
func TestFinishRun_NoIndexerWiredIsSafe(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	ms := addStubSession(m, "lumen", "P1")
	ms.session.CLISessionID = "cli-123"
	m.cfg.Optimization.ExperienceTracking = true

	m.beginRun(ms)
	m.finishRun(ms, "completed", "") // must not panic
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

// captureEmitter records every emitted event for assertions.
type captureEmitter struct {
	events []struct {
		Name string
		Data any
	}
}

func (c *captureEmitter) Emit(name string, data any) {
	c.events = append(c.events, struct {
		Name string
		Data any
	}{name, data})
}

func TestManager_ForwardsTodoEvent(t *testing.T) {
	m := newTestManager(t)
	em := &captureEmitter{}
	m.emitter = em
	ms := addStubSession(m, "lumen", "P1")

	todos := []TodoItem{
		{Content: "Step A", Status: "completed"},
		{Content: "Step B", Status: "in_progress", ActiveForm: "Doing step B"},
	}
	ms.session.updateTodos(todos)

	var todoEv *TodoEvent
	for _, e := range em.events {
		if e.Name == EventNameTodo {
			ev := e.Data.(TodoEvent)
			todoEv = &ev
		}
	}
	if todoEv == nil {
		t.Fatal("expected a session:todo event")
	}
	if todoEv.ID != "lumen/P1" {
		t.Errorf("unexpected id: %q", todoEv.ID)
	}
	if len(todoEv.Todos) != 2 || todoEv.CurrentTask != "Doing step B" {
		t.Errorf("unexpected payload: %+v", todoEv)
	}

	// The snapshot returned to the UI carries the same todo list.
	st, ok := m.GetSession("lumen/P1")
	if !ok {
		t.Fatal("GetSession failed")
	}
	if len(st.Todos) != 2 || st.CurrentTask != "Doing step B" {
		t.Errorf("unexpected session state: todos=%+v current=%q", st.Todos, st.CurrentTask)
	}
}

// TestManager_ContinueSessionQuestion_LoggedNotBlocked verifies a
// KindContinueSession marker is surfaced as a plain log entry instead of
// pausing the session — per the one-session-per-task rule the answer to
// "continue in this session?" is always no, so nobody waits around to
// answer it: the run ends exactly like any other completed turn.
func TestManager_ContinueSessionQuestion_LoggedNotBlocked(t *testing.T) {
	m := newTestManager(t)
	em := &captureEmitter{}
	m.emitter = em
	ms := addStubSession(m, "lumen", "P1")

	line := resultLineWithAskUserKind("Start S9 now?", KindContinueSession)
	done := ms.session.handleLine(line, true)
	if !done {
		t.Error("a continue_session marker must still report the turn as finished — nobody waits for it")
	}

	st, ok := m.GetSession("lumen/P1")
	if !ok {
		t.Fatal("GetSession failed")
	}
	if st.Status == "waiting_for_user" {
		t.Errorf("status = %q, must not be waiting_for_user", st.Status)
	}

	var sawQuestionLog bool
	for _, e := range em.events {
		if e.Name != EventNameLog {
			continue
		}
		if le, ok := e.Data.(LogEvent); ok && strings.Contains(le.Entry.Message, "Start S9 now?") {
			sawQuestionLog = true
		}
	}
	if !sawQuestionLog {
		t.Error("expected the question to be recorded as a log entry")
	}
}

// TestAnswerQuestionMissing verifies AnswerQuestion rejects an unknown
// session instead of panicking.
func TestAnswerQuestionMissing(t *testing.T) {
	m := newTestManager(t)
	if err := m.AnswerQuestion("nope/x", "q1", "answer"); err == nil {
		t.Error("expected error answering for unknown session")
	}
}

// TestManager_QuestionFlow verifies a default-kind (genuine decision)
// ask-user question surfaces through GetPendingQuestions/GetSession and
// clears once answered — the flow the UI banner relies on (see CLAUDE.md
// "Ask-User Questions (Autonomous Sessions)").
func TestManager_QuestionFlow(t *testing.T) {
	m := newTestManager(t)
	em := &captureEmitter{}
	m.emitter = em
	ms := addStubSession(m, "lumen", "P1")

	line := resultLineWithAskUser("Which budget?")
	ms.session.handleLine(line, true)

	pending := m.GetPendingQuestions()
	if len(pending) != 1 || pending[0].SessionID != "lumen/P1" {
		t.Fatalf("unexpected pending questions: %+v", pending)
	}
	if pending[0].Question.Question != "Which budget?" {
		t.Errorf("unexpected question text: %+v", pending[0].Question)
	}

	st, ok := m.GetSession("lumen/P1")
	if !ok {
		t.Fatal("GetSession failed")
	}
	if st.Status != "waiting_for_user" {
		t.Errorf("status = %q, want waiting_for_user", st.Status)
	}
	if st.PendingQuestion == nil || st.PendingQuestion.Question != "Which budget?" {
		t.Errorf("SessionState.PendingQuestion = %+v", st.PendingQuestion)
	}

	var sawQuestionEvent bool
	for _, e := range em.events {
		if e.Name == EventNameQuestion {
			sawQuestionEvent = true
		}
	}
	if !sawQuestionEvent {
		t.Error("expected a session:question event to be emitted")
	}

	// Answering clears the pending question even though there is no live CLI
	// process in this test to actually accept the stdin write.
	_ = m.AnswerQuestion("lumen/P1", pending[0].Question.ID, "Keep the 2ms budget")
	if got := m.GetPendingQuestions(); len(got) != 0 {
		t.Errorf("expected no pending questions after answering, got %+v", got)
	}
}
