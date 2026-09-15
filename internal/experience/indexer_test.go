package experience

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestIngestDir_Fixtures imports the whole testdata/logfiles fixture set and
// checks the aggregate counts, including the two tool_use rows from the
// broken.md fixture that still import despite its one garbage line.
func TestIngestDir_Fixtures(t *testing.T) {
	s := newTestStore(t)

	stats, err := IngestDir(s, "../../testdata/logfiles", "proj", "", ImportOpts{})
	if err != nil {
		t.Fatalf("IngestDir: %v", err)
	}
	if stats.Files != 4 {
		t.Errorf("Files = %d, want 4", stats.Files)
	}
	if stats.Runs != 4 {
		t.Errorf("Runs = %d, want 4", stats.Runs)
	}
	if stats.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0 on a first import", stats.Skipped)
	}
	// broken.md contributes exactly one malformed line.
	if stats.Errors != 1 {
		t.Errorf("Errors = %d, want 1 (the one garbage line in broken.md)", stats.Errors)
	}
	// Tool calls across all four fixtures: basic(1) + parallel(3) +
	// heartbeat-error(1) + broken(1) = 6.
	if stats.Actions != 6 {
		t.Errorf("Actions = %d, want 6", stats.Actions)
	}
}

// TestIngestDir_ReimportIsNoop: running IngestDir twice over the same
// directory must not grow action_signatures the second time — every file was
// already marked imported (LEARN-TASKS.md LN-17 "тест повторного импорта").
func TestIngestDir_ReimportIsNoop(t *testing.T) {
	s := newTestStore(t)

	first, err := IngestDir(s, "../../testdata/logfiles", "proj", "", ImportOpts{})
	if err != nil {
		t.Fatalf("IngestDir (first): %v", err)
	}
	if first.Files == 0 {
		t.Fatal("first import ingested nothing, nothing to test")
	}

	second, err := IngestDir(s, "../../testdata/logfiles", "proj", "", ImportOpts{})
	if err != nil {
		t.Fatalf("IngestDir (second): %v", err)
	}
	if second.Files != 0 {
		t.Errorf("second pass Files = %d, want 0 (already imported)", second.Files)
	}
	if second.Actions != 0 {
		t.Errorf("second pass Actions = %d, want 0", second.Actions)
	}
	if second.Skipped != first.Files {
		t.Errorf("second pass Skipped = %d, want %d (every file from the first pass)", second.Skipped, first.Files)
	}

	stats, err := s.TopSignatures("proj", 3650, 0)
	if err != nil {
		t.Fatalf("TopSignatures: %v", err)
	}
	var total int
	for _, st := range stats {
		total += st.Count
	}
	if total != first.Actions {
		t.Errorf("action_signatures row count = %d, want %d (no duplicate rows after reimport)", total, first.Actions)
	}
}

// TestIngestDir_ProgressCallback reports processed/total for every file, in
// order, ending at (N, N).
func TestIngestDir_ProgressCallback(t *testing.T) {
	s := newTestStore(t)

	var calls [][2]int
	_, err := IngestDir(s, "../../testdata/logfiles", "proj", "", ImportOpts{
		OnProgress: func(processed, total int) { calls = append(calls, [2]int{processed, total}) },
	})
	if err != nil {
		t.Fatalf("IngestDir: %v", err)
	}
	if len(calls) != 4 {
		t.Fatalf("got %d progress calls, want 4", len(calls))
	}
	for i, c := range calls {
		if c[0] != i+1 || c[1] != 4 {
			t.Errorf("call %d = %v, want (%d, 4)", i, c, i+1)
		}
	}
}

// TestIngestDir_MixedFormats: a directory holding both a JSONL transcript
// (LN-01) and a markdown log (LN-17) imports both, routed by extension.
func TestIngestDir_MixedFormats(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()

	copyFile(t, "../../testdata/transcripts/sample.jsonl", filepath.Join(dir, "sample.jsonl"))
	copyFile(t, "../../testdata/logfiles/basic.md", filepath.Join(dir, "basic.md"))
	// A file extension IngestDir doesn't recognise is ignored, not an error.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("irrelevant"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stats, err := IngestDir(s, dir, "proj", "", ImportOpts{})
	if err != nil {
		t.Fatalf("IngestDir: %v", err)
	}
	if stats.Files != 2 {
		t.Errorf("Files = %d, want 2 (one .jsonl, one .md)", stats.Files)
	}
}

// TestIngestRun_FixtureAndOffsetIdempotent is the LN-03 twin of the LN-01/17
// fixture tests above: it indexes a live run's real JSONL transcript via
// CM_TRANSCRIPTS_DIR, checking the three tool_use rows land with a real
// run_id and the resolved task pointer, then that a second call (same
// cli_session_id, transcript unchanged) is a no-op thanks to the ingest
// offset (LN-02).
func TestIngestRun_FixtureAndOffsetIdempotent(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	const cliSessionID = "11111111-1111-1111-1111-111111111111"
	copyFile(t, "../../testdata/transcripts/sample.jsonl", filepath.Join(dir, cliSessionID+".jsonl"))
	t.Setenv("CM_TRANSCRIPTS_DIR", dir)

	run := &store.SessionRun{Project: "proj", Session: "S1", Model: "sonnet", StartedAt: time.Now().UTC(), Status: "working"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	err := IngestRun(s, "proj", "S1", run.ID, cliSessionID, `D:\Project\example-app`, "STATUS-P1.md:5")
	if err != nil {
		t.Fatalf("IngestRun: %v", err)
	}

	stats, err := s.TopSignatures("proj", 3650, 0)
	if err != nil {
		t.Fatalf("TopSignatures: %v", err)
	}
	var total int
	for _, st := range stats {
		total += st.Count
	}
	// Fixture has exactly 3 tool_use steps (Bash, Read, Grep).
	if total != 3 {
		t.Fatalf("action rows = %d, want 3", total)
	}

	samples, err := s.ActionSamples("proj", "Bash:git status --short", 0)
	if err != nil {
		t.Fatalf("ActionSamples: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("ActionSamples: got %d rows, want 1", len(samples))
	}
	if samples[0].RunID == nil || *samples[0].RunID != run.ID {
		t.Errorf("RunID = %v, want %d (a live run's rows carry a real run_id)", samples[0].RunID, run.ID)
	}
	if samples[0].TaskPtr != "STATUS-P1.md:5" {
		t.Errorf("TaskPtr = %q, want the resolved task pointer", samples[0].TaskPtr)
	}
	if samples[0].CLISessionID != cliSessionID {
		t.Errorf("CLISessionID = %q, want %q", samples[0].CLISessionID, cliSessionID)
	}

	// Re-running with the same cli_session_id must not duplicate rows: the
	// transcript hasn't grown, so the saved offset already covers it all.
	if err := IngestRun(s, "proj", "S1", run.ID, cliSessionID, `D:\Project\example-app`, "STATUS-P1.md:5"); err != nil {
		t.Fatalf("IngestRun (second): %v", err)
	}
	stats2, err := s.TopSignatures("proj", 3650, 0)
	if err != nil {
		t.Fatalf("TopSignatures (second): %v", err)
	}
	var total2 int
	for _, st := range stats2 {
		total2 += st.Count
	}
	if total2 != 3 {
		t.Errorf("action rows after reimport = %d, want 3 (no duplicates)", total2)
	}
}

// TestIngestRun_EmptyCLISessionIDIsNoop: a run that never got a system/init
// event (e.g. the process died immediately) has no CLI session id and thus
// no transcript to find — IngestRun must not error, just do nothing.
func TestIngestRun_EmptyCLISessionIDIsNoop(t *testing.T) {
	s := newTestStore(t)
	if err := IngestRun(s, "proj", "S1", 1, "", "/some/path", ""); err != nil {
		t.Fatalf("IngestRun with empty cliSessionID: %v", err)
	}
	stats, err := s.TopSignatures("proj", 3650, 0)
	if err != nil {
		t.Fatalf("TopSignatures: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected no rows, got %+v", stats)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", dst, err)
	}
}

// TestIngestDir_MissingRoot: a nonexistent root directory is not an error —
// filepath.WalkDir's own root-stat error is swallowed by the best-effort
// walk, same as an unreadable subdirectory — it just finds nothing.
func TestIngestDir_MissingRoot(t *testing.T) {
	s := newTestStore(t)
	stats, err := IngestDir(s, "../../testdata/logfiles/does-not-exist", "proj", "", ImportOpts{})
	if err != nil {
		t.Fatalf("IngestDir: %v", err)
	}
	if stats.Files != 0 || stats.Errors != 0 {
		t.Errorf("stats = %+v, want all zero", stats)
	}
}

// TestActionRows_MdAndJSONLAgreeOnSignature is the LEARN-TASKS.md LN-19
// "готово когда" case: a markdown-backend Trajectory (ProjectPath always
// empty, LN-17) and a JSONL-backend Trajectory (ProjectPath set from the
// init event's cwd) must produce the identical signature for the same
// absolute Windows Read path, once the caller-supplied projectPath fallback
// is wired through actionRows.
func TestActionRows_MdAndJSONLAgreeOnSignature(t *testing.T) {
	const absPath = `D:\Project\example-app\internal\foo.go`
	const projectPath = `D:\Project\example-app`

	mdTraj := Trajectory{
		// ProjectPath empty, exactly like ParseLogFile's output (LN-17).
		Steps: []Step{{Index: 0, Kind: StepToolUse, ToolName: "Read", InputText: absPath}},
	}
	jsonlTraj := Trajectory{
		ProjectPath: projectPath,
		Steps:       []Step{{Index: 0, Kind: StepToolUse, ToolName: "Read", InputText: absPath}},
	}

	mdRows := actionRows(mdTraj, "proj", "S1", projectPath, nil, "md-file.md", "")
	jsonlRows := actionRows(jsonlTraj, "proj", "S1", "", nil, "cli-session", "")

	if len(mdRows) != 1 || len(jsonlRows) != 1 {
		t.Fatalf("got %d md rows, %d jsonl rows, want 1 each", len(mdRows), len(jsonlRows))
	}
	if mdRows[0].Sig != jsonlRows[0].Sig {
		t.Errorf("md sig = %q, jsonl sig = %q, want equal", mdRows[0].Sig, jsonlRows[0].Sig)
	}
	if mdRows[0].Sig != "Read:internal/*.go" {
		t.Errorf("sig = %q, want %q", mdRows[0].Sig, "Read:internal/*.go")
	}
}

// TestActionRows_ProjectPathPriority: traj.ProjectPath still wins over the
// caller-supplied fallback when both are set — the fallback only fills a gap,
// it never overrides a Trajectory that actually knows its own cwd.
func TestActionRows_ProjectPathPriority(t *testing.T) {
	traj := Trajectory{
		ProjectPath: `D:\Real\project`,
		Steps:       []Step{{Index: 0, Kind: StepToolUse, ToolName: "Read", InputText: `D:\Real\project\internal\foo.go`}},
	}
	rows := actionRows(traj, "proj", "S1", `D:\Wrong\fallback`, nil, "cli-1", "")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Sig != "Read:internal/*.go" {
		t.Errorf("sig = %q, want %q (traj.ProjectPath should win)", rows[0].Sig, "Read:internal/*.go")
	}
}

// TestIngestDir_BroughtCorpusNoMachinePrefix: a log brought from another
// machine (its absolute paths share no prefix with the local project's own
// path) must never leave a drive letter or a foreign temp-directory prefix
// in the resulting signature (LEARN-TASKS.md LN-19).
func TestIngestDir_BroughtCorpusNoMachinePrefix(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	md := "# Session log — brought/S1\n\n" +
		"_Saved 2026-09-08T10:15:30Z, 2 entries._\n\n" +
		"- `10:15:01` **tool** Read: D:\\temp\\project-logs-20260908\\lumen\\crates\\shell\\src\\main.rs\n" +
		"  - tool: `Read` D:\\temp\\project-logs-20260908\\lumen\\crates\\shell\\src\\main.rs\n" +
		"- `10:15:02` **tool_result** fn main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "brought.md"), []byte(md), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The local project's own path shares no prefix with the brought log at
	// all — the classic "corpus brought from a different machine" case.
	const localProjectPath = `D:\GoProjects\lumen-browser`
	stats, err := IngestDir(s, dir, "proj", localProjectPath, ImportOpts{})
	if err != nil {
		t.Fatalf("IngestDir: %v", err)
	}
	if stats.Actions != 1 {
		t.Fatalf("Actions = %d, want 1", stats.Actions)
	}

	sigStats, err := s.TopSignatures("proj", 3650, 0)
	if err != nil {
		t.Fatalf("TopSignatures: %v", err)
	}
	if len(sigStats) != 1 {
		t.Fatalf("got %d signatures, want 1", len(sigStats))
	}
	sig := sigStats[0].Sig
	if strings.Contains(sig, "D:/") || strings.Contains(sig, `D:\`) {
		t.Errorf("sig %q still carries a drive letter", sig)
	}
	if strings.Contains(sig, "temp/project-logs") {
		t.Errorf("sig %q still carries the foreign machine's temp-directory prefix", sig)
	}
	if sig != "Read:src/*.rs" {
		t.Errorf("sig = %q, want %q", sig, "Read:src/*.rs")
	}
}
