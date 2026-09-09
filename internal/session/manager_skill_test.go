package session

import (
	"context"
	"testing"

	"claude-manager/internal/analysis"
)

// stubSkillDistiller returns a fixed SkillDraft, recording the score/minScore
// it was called with — the skillDistillFn counterpart of stubStreamingAnalyzer.
func stubSkillDistiller(draft *analysis.SkillDraft) (skillDistillFn, *[]float64) {
	var scores []float64
	fn := func(_ context.Context, _ string, _ analysis.SkillDistillInput, score, minScore float64, _ analysis.AnalysisConfig, _ analysis.ProgressFunc) (*analysis.SkillDraft, error) {
		scores = append(scores, score, minScore)
		return draft, nil
	}
	return fn, &scores
}

func testSkillDraft() *analysis.SkillDraft {
	return &analysis.SkillDraft{
		Name:        "git-session-preamble",
		Description: "Use at session start to orient in the repo.",
		Steps:       []analysis.SkillStep{{Command: "git status --short --branch"}},
		DoneWhen:    "branch and status are known",
	}
}

func TestDistillSkillSavesDraftRow(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	distill, calls := stubSkillDistiller(testSkillDraft())

	in := analysis.SkillDistillInput{Sig: []string{"Bash:git status"}}
	sk, err := m.distillSkill(context.Background(), "lumen", in, `["Bash:git status"]`, 42, 10, "sonnet", distill)
	if err != nil {
		t.Fatalf("distillSkill: %v", err)
	}
	if sk.ID == 0 {
		t.Error("skill must receive a store ID on save")
	}
	if sk.Project != "lumen" || sk.Name != "git-session-preamble" || sk.Status != "draft" {
		t.Errorf("unexpected skill row: %+v", sk)
	}
	if sk.SourceJSON != `["Bash:git status"]` {
		t.Errorf("SourceJSON = %q", sk.SourceJSON)
	}
	if sk.MD == "" || sk.DraftJSON == "" {
		t.Error("expected non-empty MD and DraftJSON")
	}
	if len(*calls) != 2 || (*calls)[0] != 42 || (*calls)[1] != 10 {
		t.Errorf("distiller received unexpected score/minScore: %v", *calls)
	}

	got, err := m.store.GetSkill(sk.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got == nil || got.Name != sk.Name {
		t.Fatalf("reloaded skill mismatch: %+v", got)
	}
}

func TestDistillSkillUnknownProject(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	distill, _ := stubSkillDistiller(testSkillDraft())
	if _, err := m.distillSkill(context.Background(), "no-such", analysis.SkillDistillInput{}, "", 100, 0, "sonnet", distill); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestDistillSkillPropagatesBelowThreshold(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	distill := func(_ context.Context, _ string, _ analysis.SkillDistillInput, _, _ float64, _ analysis.AnalysisConfig, _ analysis.ProgressFunc) (*analysis.SkillDraft, error) {
		return nil, analysis.ErrBelowThreshold
	}
	_, err := m.distillSkill(context.Background(), "lumen", analysis.SkillDistillInput{}, "", 1, 10, "sonnet", distill)
	if err != analysis.ErrBelowThreshold {
		t.Fatalf("expected ErrBelowThreshold to propagate, got %v", err)
	}
}

func TestDistillSkillNoStoreConfigured(t *testing.T) {
	m := newTestManager(t) // no store wired
	distill, _ := stubSkillDistiller(testSkillDraft())
	if _, err := m.distillSkill(context.Background(), "lumen", analysis.SkillDistillInput{}, "", 100, 0, "sonnet", distill); err == nil {
		t.Fatal("expected error when no store is configured")
	}
}
