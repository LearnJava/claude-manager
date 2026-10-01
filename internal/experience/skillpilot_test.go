package experience

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
)

// pilotFixture wires a SkillPilot over an in-memory store, a temp project
// folder and stub distill/review calls that record how often they ran.
type pilotFixture struct {
	t        *testing.T
	st       *store.Store
	dir      string
	now      time.Time
	cands    []SkillCandidate
	draft    analysis.SkillDraft
	accept   bool
	distills int
	reviews  int
	pilot    *SkillPilot
}

func newPilotFixture(t *testing.T) *pilotFixture {
	t.Helper()
	f := &pilotFixture{
		t:      t,
		st:     newTestStore(t),
		dir:    t.TempDir(),
		now:    time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		accept: true,
		draft:  analysis.SkillDraft{Name: "run-gates", Description: "Use before merging.", CostUSD: 0.2},
		cands: []SkillCandidate{{
			Sig: []string{"Bash:go build ./...", "Bash:go test ./..."}, Kind: KindSkill,
			Score: 50, RunShare: 0.4, DistinctRuns: 8,
		}},
	}
	f.pilot = NewSkillPilot(SkillPilotDeps{
		Store: f.st,
		Now:   func() time.Time { return f.now },
		Distill: func(context.Context, string, analysis.SkillDistillInput) (*analysis.SkillDraft, error) {
			f.distills++
			d := f.draft
			return &d, nil
		},
		Review: func(context.Context, string, analysis.SkillReviewInput) (*analysis.SkillReview, error) {
			f.reviews++
			return &analysis.SkillReview{Accept: f.accept, Reason: "ok", CostUSD: 0.1}, nil
		},
		Candidates: func(*store.Store, string) ([]SkillCandidate, error) { return f.cands, nil },
	})
	return f
}

func (f *pilotFixture) tick() SkillPilotReport {
	f.t.Helper()
	rep, err := f.pilot.Tick(context.Background(), SkillPilotParams{Project: "proj", ProjectPath: f.dir})
	if err != nil {
		f.t.Fatalf("Tick: %v", err)
	}
	return rep
}

func (f *pilotFixture) only() store.Skill {
	f.t.Helper()
	skills, err := f.st.ListSkills("proj")
	if err != nil || len(skills) != 1 {
		f.t.Fatalf("ListSkills = %d rows, %v; want 1", len(skills), err)
	}
	return skills[0]
}

func (f *pilotFixture) skillFile(name string) string {
	return filepath.Join(f.dir, ".claude", "skills", name, "SKILL.md")
}

// addRuns inserts n indexed runs starting after `from`; loadEvery > 0 makes
// every loadEvery-th run load skill `name`.
func (f *pilotFixture) addRuns(n int, from time.Time, name string, loadEvery int) {
	f.t.Helper()
	for i := 0; i < n; i++ {
		r := mkRunWithSig(f.t, f.st, "proj", from.Add(time.Duration(i+1)*time.Minute), 1000, 3, "completed", "Bash:git status")
		if loadEvery > 0 && (i+1)%loadEvery == 0 {
			mkSkillLoad(f.t, f.st, "proj", r, name, r.StartedAt.Add(time.Second))
		}
	}
}

func reasonOf(t *testing.T, sk store.Skill) SkillReason {
	t.Helper()
	var r SkillReason
	if err := json.Unmarshal([]byte(sk.Reason), &r); err != nil {
		t.Fatalf("decode reason %q: %v", sk.Reason, err)
	}
	return r
}

func TestSkillPilot_AppliesAcceptedDraftOnTrial(t *testing.T) {
	f := newPilotFixture(t)
	rep := f.tick()
	if len(rep.Applied) != 1 || rep.Applied[0] != "run-gates" {
		t.Fatalf("report = %+v, want run-gates applied", rep)
	}
	sk := f.only()
	if sk.Status != store.SkillStatusTrial || sk.Origin != store.SkillOriginAuto || sk.ApprovedAt == nil {
		t.Fatalf("row = %+v, want auto trial with ApprovedAt", sk)
	}
	if got := reasonOf(t, sk).Code; got != ReasonAppliedReview {
		t.Errorf("reason code = %q, want %q", got, ReasonAppliedReview)
	}
	if sk.CostUSD < 0.29 || sk.CostUSD > 0.31 {
		t.Errorf("CostUSD = %v, want distill+review 0.3", sk.CostUSD)
	}
	if _, err := os.Stat(f.skillFile("run-gates")); err != nil {
		t.Errorf("skill file not written: %v", err)
	}
}

func TestSkillPilot_ReviewRejectionWritesNothing(t *testing.T) {
	f := newPilotFixture(t)
	f.accept = false
	rep := f.tick()
	if len(rep.Rejected) != 1 {
		t.Fatalf("report = %+v, want one rejection", rep)
	}
	sk := f.only()
	if sk.Status != store.SkillStatusRejected || reasonOf(t, sk).Code != ReasonReviewRejected {
		t.Fatalf("row = %+v, want rejected/review_rejected", sk)
	}
	if _, err := os.Stat(f.skillFile("run-gates")); !os.IsNotExist(err) {
		t.Errorf("rejected skill was written to the project")
	}
}

func TestSkillPilot_NameTakenSkipsReview(t *testing.T) {
	f := newPilotFixture(t)
	if _, err := WriteSkillFile(f.dir, "run-gates", "---\nname: run-gates\ndescription: mine\n---\n", false); err != nil {
		t.Fatal(err)
	}
	f.tick()
	sk := f.only()
	if sk.Status != store.SkillStatusRejected || reasonOf(t, sk).Code != ReasonNameTaken {
		t.Fatalf("row = %+v, want rejected/name_taken", sk)
	}
	if f.reviews != 0 {
		t.Errorf("reviewer called %d times, want 0", f.reviews)
	}
	data, _ := os.ReadFile(f.skillFile("run-gates"))
	if string(data) != "---\nname: run-gates\ndescription: mine\n---\n" {
		t.Errorf("the user's own skill was overwritten: %q", data)
	}
}

func TestSkillPilot_OneTrialAtATime(t *testing.T) {
	f := newPilotFixture(t)
	f.tick()
	f.cands = append(f.cands, SkillCandidate{Sig: []string{"Bash:make lint"}, Kind: KindSkill, Score: 60})
	f.addRuns(AutoDistillGapRuns+1, f.now, "", 0)
	rep := f.tick()
	if rep.Idle != "trial_in_progress" || f.distills != 1 {
		t.Fatalf("report = %+v, distills = %d; want idle trial_in_progress after one distill", rep, f.distills)
	}
}

func TestSkillPilot_NeverRedistillsAKnownSequence(t *testing.T) {
	f := newPilotFixture(t)
	f.accept = false
	f.tick()
	f.addRuns(AutoDistillGapRuns+1, f.now, "", 0)
	rep := f.tick()
	if rep.Idle != "no_candidate" || f.distills != 1 {
		t.Fatalf("report = %+v, distills = %d; want no_candidate", rep, f.distills)
	}
}

func TestSkillPilot_GapBetweenDistillations(t *testing.T) {
	f := newPilotFixture(t)
	f.accept = false
	f.tick()
	f.cands = append(f.cands, SkillCandidate{Sig: []string{"Bash:make lint"}, Kind: KindSkill, Score: 60})
	f.addRuns(AutoDistillGapRuns-1, f.now, "", 0)
	if rep := f.tick(); rep.Idle != "gap" {
		t.Fatalf("report = %+v, want idle gap", rep)
	}
	f.addRuns(1, f.now.Add(time.Hour), "", 0)
	if rep := f.tick(); len(rep.Rejected) != 1 {
		t.Fatalf("report = %+v, want a second distillation after the gap", rep)
	}
}

func TestSkillPilot_DailyBudget(t *testing.T) {
	f := newPilotFixture(t)
	f.accept = false
	f.tick() // spends 0.3
	f.cands = append(f.cands, SkillCandidate{Sig: []string{"Bash:make lint"}, Kind: KindSkill, Score: 60})
	f.addRuns(AutoDistillGapRuns, f.now, "", 0)
	rep, err := f.pilot.Tick(context.Background(), SkillPilotParams{Project: "proj", ProjectPath: f.dir, DailyBudgetUSD: 0.25})
	if err != nil || rep.Idle != "budget" {
		t.Fatalf("report = %+v, %v; want idle budget", rep, err)
	}
}

func TestSkillPilot_UnusedTrialIsSwitchedOff(t *testing.T) {
	f := newPilotFixture(t)
	f.tick()
	f.addRuns(SkillTrialRuns, f.now, "", 0)
	f.cands = nil
	rep := f.tick()
	if len(rep.Archived) != 1 {
		t.Fatalf("report = %+v, want run-gates archived", rep)
	}
	sk := f.only()
	if sk.Status != store.SkillStatusArchived || reasonOf(t, sk).Code != ReasonUnusedTrial {
		t.Fatalf("row = %+v, want archived/unused_trial", sk)
	}
	if _, err := os.Stat(f.skillFile("run-gates")); !os.IsNotExist(err) {
		t.Error("archived skill is still visible to the CLI")
	}
	arch, _ := ArchivedSkillPath(f.dir, "run-gates", sk.ID)
	if _, err := os.Stat(filepath.Join(arch, "SKILL.md")); err != nil {
		t.Errorf("archived copy missing: %v", err)
	}
}

func TestSkillPilot_TrialNotOverBeforeEnoughRuns(t *testing.T) {
	f := newPilotFixture(t)
	f.tick()
	f.addRuns(SkillTrialRuns-1, f.now, "", 0)
	f.tick()
	if sk := f.only(); sk.Status != store.SkillStatusTrial {
		t.Fatalf("status = %q after %d runs, want still trial", sk.Status, SkillTrialRuns-1)
	}
}

func TestSkillPilot_LoadedTrialIsKept(t *testing.T) {
	f := newPilotFixture(t)
	f.tick()
	f.addRuns(SkillTrialRuns, f.now, "run-gates", 3)
	f.cands = nil
	rep := f.tick()
	if len(rep.Kept) != 1 {
		t.Fatalf("report = %+v, want run-gates kept", rep)
	}
	sk := f.only()
	r := reasonOf(t, sk)
	if sk.Status != store.SkillStatusApproved || r.Code != ReasonKept || r.Loads != 3 || r.Runs != SkillTrialRuns {
		t.Fatalf("row = %+v reason %+v, want approved/kept 3 loads in %d runs", sk, r, SkillTrialRuns)
	}
}

func TestSkillPilot_KeptSkillUnusedLatelyIsSwitchedOff(t *testing.T) {
	f := newPilotFixture(t)
	f.tick()
	f.addRuns(SkillTrialRuns, f.now, "run-gates", 2)
	f.cands = nil
	f.tick()
	f.addRuns(StaleRunWindow, f.now.Add(time.Hour), "", 0)
	rep := f.tick()
	if len(rep.Archived) != 1 || reasonOf(t, f.only()).Code != ReasonUnusedLately {
		t.Fatalf("report = %+v, want archived/unused_lately", rep)
	}
}

func TestSkillPilot_ReviewsPendingDraftBeforeDistilling(t *testing.T) {
	f := newPilotFixture(t)
	draft := &store.Skill{Project: "proj", Name: "peek-lines", Status: store.SkillStatusDraft,
		DraftJSON: "{}", MD: "---\nname: peek-lines\n---\n", SourceJSON: `["Bash:sed -n <ARG>"]`, CreatedAt: f.now.Add(-time.Hour)}
	if err := f.st.InsertSkill(draft); err != nil {
		t.Fatal(err)
	}
	rep := f.tick()
	if len(rep.Applied) != 1 || rep.Applied[0] != "peek-lines" || f.distills != 0 {
		t.Fatalf("report = %+v, distills = %d; want the draft applied without distilling", rep, f.distills)
	}
	got, _ := f.st.GetSkill(draft.ID)
	if got.Status != store.SkillStatusTrial || got.Origin != store.SkillOriginManual {
		t.Fatalf("draft row = %+v, want trial, origin kept manual", got)
	}
	if _, err := os.Stat(f.skillFile("peek-lines")); err != nil {
		t.Errorf("draft not written: %v", err)
	}
}

func TestSkillPilot_DistillFailureIsRecorded(t *testing.T) {
	f := newPilotFixture(t)
	f.pilot.deps.Distill = func(context.Context, string, analysis.SkillDistillInput) (*analysis.SkillDraft, error) {
		return nil, errors.New("claude: rate limited")
	}
	f.tick()
	sk := f.only()
	if sk.Status != store.SkillStatusRejected || reasonOf(t, sk).Code != ReasonDistillFailed {
		t.Fatalf("row = %+v, want rejected/distill_failed", sk)
	}
}

func TestSkillNameFromLoad(t *testing.T) {
	cases := []struct{ tool, arg, want string }{
		{"Skill", `{"skill":"cm-task-start","args":"P1"}`, "cm-task-start"},
		{"Skill", `{"skill":"cm-task-finish","arg`, "cm-task-finish"},
		{"Skill", `{"skill":"plugin:deploy"}`, "deploy"},
		{"Skill", `peek-lines`, "peek-lines"},
		{"skill_view", "lumen-task-finish", "lumen-task-finish"},
		{"Bash", "ls", ""},
	}
	for _, c := range cases {
		if got := SkillNameFromLoad(c.tool, c.arg); got != c.want {
			t.Errorf("SkillNameFromLoad(%q, %q) = %q, want %q", c.tool, c.arg, got, c.want)
		}
	}
}

func TestSigKey_NormalizesEscaping(t *testing.T) {
	a := SigKey(`["Bash:sed -n <ARG>"]`)
	b := SigKey(`["Bash:sed -n \u003cARG\u003e"]`)
	if a != b {
		t.Errorf("SigKey differs: %q vs %q", a, b)
	}
}

func TestArchiveRestoreSkillFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteSkillFile(dir, "peek", "edited by hand", false); err != nil {
		t.Fatal(err)
	}
	if err := ArchiveSkillFile(dir, "peek", 7); err != nil {
		t.Fatalf("ArchiveSkillFile: %v", err)
	}
	live, _ := SkillFilePath(dir, "peek")
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatal("file still live after archive")
	}
	if err := ArchiveSkillFile(dir, "peek", 7); err != nil {
		t.Fatalf("archiving an absent skill must not fail: %v", err)
	}
	path, err := RestoreSkillFile(dir, "peek", 7, "fallback", false)
	if err != nil {
		t.Fatalf("RestoreSkillFile: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "edited by hand" {
		t.Errorf("restored %q, want the archived (hand-edited) copy", data)
	}
	if _, err := RestoreSkillFile(dir, "peek", 7, "fallback", false); err != ErrSkillFileExists {
		t.Errorf("restore over a live skill = %v, want ErrSkillFileExists", err)
	}
}

func TestListProjectSkills_ReadsDescriptions(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteSkillFile(dir, "a-skill", "---\nname: a-skill\ndescription: \"Use when X.\"\n---\nbody", false); err != nil {
		t.Fatal(err)
	}
	got := ListProjectSkills(dir)
	if len(got) != 1 || got[0].Name != "a-skill" || got[0].Description != "Use when X." {
		t.Fatalf("ListProjectSkills = %+v", got)
	}
}

func TestBuildSkillUsage_CountsOnlyIndexedRunsSinceApplied(t *testing.T) {
	s := newTestStore(t)
	approvedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	sk := mkApprovedSkill(t, s, "proj", "peek", []string{"Bash:x"}, approvedAt)
	before := mkRunWithSig(t, s, "proj", approvedAt.Add(-time.Hour), 1, 1, "completed", "Bash:y")
	mkSkillLoad(t, s, "proj", before, "peek", before.StartedAt.Add(time.Second))
	r1 := mkRunWithSig(t, s, "proj", approvedAt.Add(time.Hour), 1, 1, "completed", "Bash:y")
	mkSkillLoad(t, s, "proj", r1, "peek", r1.StartedAt.Add(time.Second))
	mkRunWithSig(t, s, "proj", approvedAt.Add(2*time.Hour), 1, 1, "completed", "Bash:y")
	// An unindexed run (no action rows) must not count.
	if err := s.InsertRun(&store.SessionRun{Project: "proj", Session: "S1", StartedAt: approvedAt.Add(3 * time.Hour), Status: "completed"}); err != nil {
		t.Fatal(err)
	}

	usage, err := BuildSkillUsage(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	u := usage[sk.ID]
	if u.Loads != 2 || u.RunsSinceApplied != 2 || u.RunsWithLoad != 1 || u.RunsSinceLastLoad != 1 {
		t.Fatalf("usage = %+v, want loads 2, runs 2, with load 1, since last load 1", u)
	}
}

func TestSkillPilot_TrialThatMadeRunsWorseIsSwitchedOff(t *testing.T) {
	f := newPilotFixture(t)
	sig := f.cands[0].Sig[0]
	for i := 0; i < 3; i++ {
		mkRunWithSig(t, f.st, "proj", f.now.Add(-time.Duration(i+1)*time.Hour), 1000, 3, "completed", sig)
	}
	f.tick()
	for i := 0; i < SkillTrialRuns; i++ {
		r := mkRunWithSig(t, f.st, "proj", f.now.Add(time.Duration(i+1)*time.Minute), 5000, 9, "completed", sig)
		mkSkillLoad(t, f.st, "proj", r, "run-gates", r.StartedAt.Add(time.Second))
	}
	f.cands = nil
	rep := f.tick()
	skills, _ := f.st.ListSkills("proj")
	if len(rep.Archived) != 1 || reasonOf(t, skills[0]).Code != ReasonWorse {
		t.Fatalf("report = %+v, row = %+v; want archived/worse", rep, skills[0])
	}
}
