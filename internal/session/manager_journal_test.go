package session

import (
	"context"
	"testing"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
)

// stubJournalAnalyzer returns a fixed JournalResult, recording the received
// input for assertions.
func stubJournalAnalyzer(result *analysis.JournalResult) (journalAnalyzeFn, *[]analysis.JournalInput) {
	var inputs []analysis.JournalInput
	fn := func(_ context.Context, _ string, in analysis.JournalInput, _ analysis.AnalysisConfig) (*analysis.JournalResult, error) {
		inputs = append(inputs, in)
		return result, nil
	}
	return fn, &inputs
}

func setProjectJournal(m *SessionManager, project string, journal, commit bool) {
	for i := range m.cfg.Projects {
		if m.cfg.Projects[i].Name == project {
			m.cfg.Projects[i].Journal = journal
			m.cfg.Projects[i].JournalCommit = commit
			return
		}
	}
}

// journalWriteCall captures one JournalWriteFunc invocation.
type journalWriteCall struct {
	projectPath string
	commit      bool
	entry       JournalEntry
}

// TestFinishRun_WritesJournalWhenEnabled: with Journal=true and both a writer
// and analyzer wired, a completed run distills and persists exactly one
// journal entry carrying the run's task pointer, files changed and the
// analyst's result (LEARN-TASKS.md LN-06).
func TestFinishRun_WritesJournalWhenEnabled(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	setProjectJournal(m, "lumen", true, false)

	ms := addStubSession(m, "lumen", "P1")
	ms.session.taskSourceDesc = "ROADMAP.md:92"

	analyze, inputs := stubJournalAnalyzer(&analysis.JournalResult{
		Done:      "did the thing",
		Surprises: []string{"a surprise"},
		Avoid:     []string{"avoid this"},
	})
	m.journalAnalyze = analyze

	calls := make(chan journalWriteCall, 1)
	m.SetJournalWriter(func(projectPath string, commit bool, entry JournalEntry) error {
		calls <- journalWriteCall{projectPath, commit, entry}
		return nil
	})

	m.beginRun(ms)
	ms.mu.Lock()
	ms.pendingLogs = append(ms.pendingLogs,
		store.LogEntry{Timestamp: time.Now(), Level: "tool", ToolName: "Edit", ToolInput: "internal/foo.go"},
		store.LogEntry{Timestamp: time.Now(), Level: "tool", ToolName: "Read", ToolInput: "internal/foo.go"},
		store.LogEntry{Timestamp: time.Now(), Level: "text", Message: "hi"},
	)
	ms.lastResultText = "Implemented the retry loop."
	ms.mu.Unlock()

	m.finishRun(ms, "completed", "")

	select {
	case c := <-calls:
		if c.projectPath != ms.session.ProjectPath {
			t.Errorf("projectPath = %q, want %q", c.projectPath, ms.session.ProjectPath)
		}
		if c.commit {
			t.Error("commit should be false (JournalCommit not set)")
		}
		if c.entry.TaskPtr != "ROADMAP.md:92" {
			t.Errorf("entry.TaskPtr = %q", c.entry.TaskPtr)
		}
		if c.entry.Done != "did the thing" {
			t.Errorf("entry.Done = %q", c.entry.Done)
		}
		if len(c.entry.Surprises) != 1 || c.entry.Surprises[0] != "a surprise" {
			t.Errorf("entry.Surprises = %v", c.entry.Surprises)
		}
		if len(c.entry.Avoid) != 1 || c.entry.Avoid[0] != "avoid this" {
			t.Errorf("entry.Avoid = %v", c.entry.Avoid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the journal writer to be called")
	}

	if len(*inputs) != 1 {
		t.Fatalf("analyzer called %d times, want 1", len(*inputs))
	}
	in := (*inputs)[0]
	if in.TaskPtr != "ROADMAP.md:92" {
		t.Errorf("analyzer input TaskPtr = %q", in.TaskPtr)
	}
	if in.ResultText != "Implemented the retry loop." {
		t.Errorf("analyzer input ResultText = %q", in.ResultText)
	}
	if len(in.FilesChanged) != 1 || in.FilesChanged[0] != "internal/foo.go" {
		t.Errorf("analyzer input FilesChanged = %v, want just the Edit path (Read excluded, dedup'd)", in.FilesChanged)
	}
}

// TestFinishRun_NoJournalWhenProjectOptedOut: Journal=false (the default)
// must mean the analyst is never invoked, even with a writer wired — mirrors
// TestFinishRun_NoIndexingWhenTrackingDisabled's "not just discarded"
// invariant for LN-03.
func TestFinishRun_NoJournalWhenProjectOptedOut(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	ms := addStubSession(m, "lumen", "P1")

	analyzeCalled := make(chan struct{}, 1)
	m.journalAnalyze = func(_ context.Context, _ string, _ analysis.JournalInput, _ analysis.AnalysisConfig) (*analysis.JournalResult, error) {
		analyzeCalled <- struct{}{}
		return &analysis.JournalResult{Done: "x"}, nil
	}
	m.SetJournalWriter(func(string, bool, JournalEntry) error { return nil })

	m.beginRun(ms)
	m.finishRun(ms, "completed", "")

	select {
	case <-analyzeCalled:
		t.Fatal("analyst must not run with Journal=false")
	case <-time.After(100 * time.Millisecond):
		// expected: nothing happened
	}
}

// TestFinishRun_NoJournalWithoutWriter: Journal=true but no writer wired
// (app.go never called SetJournalWriter) must not panic, and must not invoke
// the analyst either.
func TestFinishRun_NoJournalWithoutWriter(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	setProjectJournal(m, "lumen", true, false)
	ms := addStubSession(m, "lumen", "P1")

	analyzeCalled := make(chan struct{}, 1)
	m.journalAnalyze = func(_ context.Context, _ string, _ analysis.JournalInput, _ analysis.AnalysisConfig) (*analysis.JournalResult, error) {
		analyzeCalled <- struct{}{}
		return &analysis.JournalResult{Done: "x"}, nil
	}

	m.beginRun(ms)
	m.finishRun(ms, "completed", "") // must not panic

	select {
	case <-analyzeCalled:
		t.Fatal("analyst must not run without a wired journal writer")
	case <-time.After(100 * time.Millisecond):
		// expected: nothing happened
	}
}

// TestFinishRun_NoJournalOnNonCompletedStatus: an errored or stopped run
// must not produce a journal entry — there is nothing distilled "done" to
// record.
func TestFinishRun_NoJournalOnNonCompletedStatus(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	setProjectJournal(m, "lumen", true, false)
	ms := addStubSession(m, "lumen", "P1")

	analyzeCalled := make(chan struct{}, 1)
	m.journalAnalyze = func(_ context.Context, _ string, _ analysis.JournalInput, _ analysis.AnalysisConfig) (*analysis.JournalResult, error) {
		analyzeCalled <- struct{}{}
		return &analysis.JournalResult{Done: "x"}, nil
	}
	m.SetJournalWriter(func(string, bool, JournalEntry) error { return nil })

	m.beginRun(ms)
	m.finishRun(ms, "error", "boom")

	select {
	case <-analyzeCalled:
		t.Fatal("analyst must not run for a non-completed status")
	case <-time.After(100 * time.Millisecond):
		// expected: nothing happened
	}
}

// TestFilesChangedFromLogs: only Edit/Write tool entries contribute, deduped,
// in first-seen order.
func TestFilesChangedFromLogs(t *testing.T) {
	logs := []store.LogEntry{
		{Level: "tool", ToolName: "Read", ToolInput: "a.go"},
		{Level: "tool", ToolName: "Edit", ToolInput: "b.go"},
		{Level: "tool", ToolName: "Write", ToolInput: "c.go"},
		{Level: "tool", ToolName: "Edit", ToolInput: "b.go"}, // duplicate
		{Level: "text", Message: "hi"},
		{Level: "tool", ToolName: "Bash", ToolInput: "go build ./..."},
	}
	got := filesChangedFromLogs(logs)
	want := []string{"b.go", "c.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
