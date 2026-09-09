package experience

import (
	"encoding/json"
	"testing"
	"time"

	"claude-manager/internal/store"
)

// mkApprovedSkill inserts a draft skill and immediately approves it at
// approvedAt, with sourceJSON encoding sigs — the shape app.go's
// skillDistillInputFromCandidate produces (LEARN-TASKS.md LN-09).
func mkApprovedSkill(t *testing.T, s *store.Store, project, name string, sigs []string, approvedAt time.Time) *store.Skill {
	t.Helper()
	srcJSON, err := json.Marshal(sigs)
	if err != nil {
		t.Fatalf("marshal sigs: %v", err)
	}
	sk := &store.Skill{
		Project: project, Name: name, Status: "draft",
		DraftJSON: "{}", MD: "body", SourceJSON: string(srcJSON),
		CreatedAt: approvedAt.Add(-24 * time.Hour),
	}
	if err := s.InsertSkill(sk); err != nil {
		t.Fatalf("InsertSkill: %v", err)
	}
	if err := s.UpdateSkillApproved(sk.ID, "body", approvedAt); err != nil {
		t.Fatalf("UpdateSkillApproved: %v", err)
	}
	got, err := s.GetSkill(sk.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	return got
}

// mkRunWithSig inserts one session_runs row plus a single action_signatures
// row carrying sig, so RunsWithSignature will mark the run comparable.
func mkRunWithSig(t *testing.T, s *store.Store, project string, when time.Time, tokens int64, turns int, status, sig string) *store.SessionRun {
	t.Helper()
	r := &store.SessionRun{
		Project: project, Session: "S1", Model: "sonnet",
		StartedAt: when, Status: status, InputTokens: tokens, NumTurns: turns,
	}
	if err := s.InsertRun(r); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	row := store.ActionRow{
		Project: project, Session: "S1", RunID: &r.ID, StepIndex: 0,
		Tool: "Bash", Sig: sig, Timestamp: when,
	}
	if err := s.InsertActions([]store.ActionRow{row}); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}
	return r
}

// TestBuildSkillQualityReport_Improvement: after-approval runs use fewer
// tokens and complete more reliably than before — a skill that is clearly
// paying for itself, not stale (LEARN-TASKS.md LN-11 "тест на улучшение").
func TestBuildSkillQualityReport_Improvement(t *testing.T) {
	s := newTestStore(t)
	sig := "Bash:go test"
	approvedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mkApprovedSkill(t, s, "proj", "run-go-tests", []string{sig}, approvedAt)

	before := approvedAt.Add(-time.Hour)
	mkRunWithSig(t, s, "proj", before, 10000, 12, "completed", sig)
	mkRunWithSig(t, s, "proj", before, 12000, 14, "completed", sig)
	mkRunWithSig(t, s, "proj", before, 11000, 10, "error", sig)

	after := approvedAt.Add(time.Hour)
	mkRunWithSig(t, s, "proj", after, 4000, 5, "completed", sig)
	mkRunWithSig(t, s, "proj", after, 3000, 4, "completed", sig)
	mkRunWithSig(t, s, "proj", after, 5000, 6, "completed", sig)

	report, err := BuildSkillQualityReport(s, "proj")
	if err != nil {
		t.Fatalf("BuildSkillQualityReport: %v", err)
	}
	if len(report) != 1 {
		t.Fatalf("report has %d entries, want 1: %+v", len(report), report)
	}
	eff := report[0]
	if eff.InsufficientData {
		t.Error("InsufficientData = true, want false (3 runs each side)")
	}
	if eff.Before.Runs != 3 || eff.After.Runs != 3 {
		t.Errorf("Before/After.Runs = %d/%d, want 3/3", eff.Before.Runs, eff.After.Runs)
	}
	if eff.Before.MedianInputTokens != 11000 {
		t.Errorf("Before.MedianInputTokens = %v, want 11000", eff.Before.MedianInputTokens)
	}
	if eff.After.MedianInputTokens != 4000 {
		t.Errorf("After.MedianInputTokens = %v, want 4000", eff.After.MedianInputTokens)
	}
	if eff.After.CompletedRate <= eff.Before.CompletedRate {
		t.Errorf("After.CompletedRate (%v) did not improve over Before (%v)", eff.After.CompletedRate, eff.Before.CompletedRate)
	}
	if eff.Stale {
		t.Errorf("expected not stale, got reason %q", eff.StaleReason)
	}
}

// TestBuildSkillQualityReport_NoImprovement: >=5 comparable runs after
// approval whose median token cost did not go down must be flagged
// "предложить в архив" / no_improvement (LEARN-TASKS.md LN-11 "тест на
// ухудшение").
func TestBuildSkillQualityReport_NoImprovement(t *testing.T) {
	s := newTestStore(t)
	sig := "Bash:cargo build"
	approvedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mkApprovedSkill(t, s, "proj", "cargo-build-skill", []string{sig}, approvedAt)

	before := approvedAt.Add(-time.Hour)
	for i := 0; i < 3; i++ {
		mkRunWithSig(t, s, "proj", before, 5000, 8, "completed", sig)
	}

	after := approvedAt.Add(time.Hour)
	for i := 0; i < 5; i++ {
		mkRunWithSig(t, s, "proj", after, 6000, 9, "completed", sig)
	}

	report, err := BuildSkillQualityReport(s, "proj")
	if err != nil {
		t.Fatalf("BuildSkillQualityReport: %v", err)
	}
	if len(report) != 1 {
		t.Fatalf("report has %d entries, want 1: %+v", len(report), report)
	}
	eff := report[0]
	if eff.InsufficientData {
		t.Error("InsufficientData = true, want false")
	}
	if !eff.Stale || eff.StaleReason != StaleReasonNoImprovement {
		t.Errorf("Stale/StaleReason = %v/%q, want true/%q", eff.Stale, eff.StaleReason, StaleReasonNoImprovement)
	}
}

// TestBuildSkillQualityReport_InsufficientData: fewer than
// MinSkillEffectRuns comparable runs on the "before" side must produce
// InsufficientData=true and, since the after-run count never reaches
// StaleMinRunsAfter either, no stale verdict at all (LEARN-TASKS.md LN-11
// "тест на мало данных").
func TestBuildSkillQualityReport_InsufficientData(t *testing.T) {
	s := newTestStore(t)
	sig := "Bash:npm test"
	approvedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mkApprovedSkill(t, s, "proj", "npm-test-skill", []string{sig}, approvedAt)

	before := approvedAt.Add(-time.Hour)
	mkRunWithSig(t, s, "proj", before, 5000, 8, "completed", sig)
	mkRunWithSig(t, s, "proj", before, 5000, 8, "completed", sig)

	after := approvedAt.Add(time.Hour)
	mkRunWithSig(t, s, "proj", after, 2000, 4, "completed", sig)
	mkRunWithSig(t, s, "proj", after, 2000, 4, "completed", sig)
	mkRunWithSig(t, s, "proj", after, 2000, 4, "completed", sig)

	report, err := BuildSkillQualityReport(s, "proj")
	if err != nil {
		t.Fatalf("BuildSkillQualityReport: %v", err)
	}
	if len(report) != 1 {
		t.Fatalf("report has %d entries, want 1: %+v", len(report), report)
	}
	eff := report[0]
	if !eff.InsufficientData {
		t.Error("InsufficientData = false, want true (only 2 before-runs)")
	}
	if eff.Stale {
		t.Errorf("expected no stale verdict with insufficient data, got reason %q", eff.StaleReason)
	}
}

// TestBuildSkillQualityReport_NeverUsed: a skill whose signature never
// occurs in any project run must be flagged stale/unused, not merely
// insufficient data — this is the "не сработал ни разу" case (LEARN-TASKS.md
// LN-11 "тест на скилл ни разу не сработал").
func TestBuildSkillQualityReport_NeverUsed(t *testing.T) {
	s := newTestStore(t)
	approvedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mkApprovedSkill(t, s, "proj", "unused-skill", []string{"Bash:some-command-nobody-runs"}, approvedAt)

	// Unrelated runs exist in the project (so the recent-run window is
	// non-empty), but none of them ever calls the skill's signature.
	mkRunWithSig(t, s, "proj", approvedAt.Add(time.Hour), 1000, 2, "completed", "Bash:git status")

	report, err := BuildSkillQualityReport(s, "proj")
	if err != nil {
		t.Fatalf("BuildSkillQualityReport: %v", err)
	}
	if len(report) != 1 {
		t.Fatalf("report has %d entries, want 1: %+v", len(report), report)
	}
	eff := report[0]
	if !eff.InsufficientData {
		t.Error("InsufficientData = false, want true (zero comparable runs)")
	}
	if !eff.Stale || eff.StaleReason != StaleReasonUnused {
		t.Errorf("Stale/StaleReason = %v/%q, want true/%q", eff.Stale, eff.StaleReason, StaleReasonUnused)
	}
}

// TestBuildSkillQualityReport_SkipsUnapproved: a draft (or a skill archived
// before ever being approved) has no ApprovedAt to split runs on and must
// not appear in the report at all.
func TestBuildSkillQualityReport_SkipsUnapproved(t *testing.T) {
	s := newTestStore(t)
	sig := "Bash:go vet"
	srcJSON, _ := json.Marshal([]string{sig})
	sk := &store.Skill{
		Project: "proj", Name: "draft-skill", Status: "draft",
		DraftJSON: "{}", MD: "body", SourceJSON: string(srcJSON),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.InsertSkill(sk); err != nil {
		t.Fatalf("InsertSkill: %v", err)
	}
	mkRunWithSig(t, s, "proj", time.Now().UTC(), 1000, 2, "completed", sig)

	report, err := BuildSkillQualityReport(s, "proj")
	if err != nil {
		t.Fatalf("BuildSkillQualityReport: %v", err)
	}
	if len(report) != 0 {
		t.Errorf("report = %+v, want empty (skill never approved)", report)
	}
}

// TestBuildSkillQualityReport_ImportedRunsExcluded: a bulk-imported row
// (LEARN-TASKS.md LN-17) has run_id=NULL and no session_runs entry, so it
// must never contribute to Before/After — otherwise "before" would count
// evidence with no real token/status to measure (LEARN-TASKS.md LN-11
// "Импортированные прогоны в замер не входят").
func TestBuildSkillQualityReport_ImportedRunsExcluded(t *testing.T) {
	s := newTestStore(t)
	sig := "Bash:make test"
	approvedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mkApprovedSkill(t, s, "proj", "make-test-skill", []string{sig}, approvedAt)

	// Only imported evidence exists before approval — no session_runs row at
	// all on that side.
	before := approvedAt.Add(-time.Hour)
	importedRows := []store.ActionRow{
		{Project: "proj", Session: "S1", RunID: nil, CLISessionID: "cli-import-1", StepIndex: 0, Tool: "Bash", Sig: sig, Timestamp: before},
		{Project: "proj", Session: "S1", RunID: nil, CLISessionID: "cli-import-2", StepIndex: 0, Tool: "Bash", Sig: sig, Timestamp: before},
		{Project: "proj", Session: "S1", RunID: nil, CLISessionID: "cli-import-3", StepIndex: 0, Tool: "Bash", Sig: sig, Timestamp: before},
	}
	if err := s.InsertActions(importedRows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	after := approvedAt.Add(time.Hour)
	mkRunWithSig(t, s, "proj", after, 1000, 2, "completed", sig)
	mkRunWithSig(t, s, "proj", after, 1000, 2, "completed", sig)
	mkRunWithSig(t, s, "proj", after, 1000, 2, "completed", sig)

	report, err := BuildSkillQualityReport(s, "proj")
	if err != nil {
		t.Fatalf("BuildSkillQualityReport: %v", err)
	}
	if len(report) != 1 {
		t.Fatalf("report has %d entries, want 1: %+v", len(report), report)
	}
	eff := report[0]
	if eff.Before.Runs != 0 {
		t.Errorf("Before.Runs = %d, want 0 (imported rows must not count)", eff.Before.Runs)
	}
	if !eff.InsufficientData {
		t.Error("InsufficientData = false, want true (no real before-side evidence)")
	}
}
