package store

import "testing"

func TestReferenceCasesLifecycle(t *testing.T) {
	s := newTestStore(t)
	c := &ReferenceCase{Project: "p", TaskPtr: "T.md:10", TaskText: "## T-1: do it", BaseCommit: "abc",
		GateCmds: []string{"go build ./...", "go test ./..."}, ResultCommit: "def", Rounds: 1,
		Tokens: 1234, DurationSec: 90, Model: "opus", Effort: "high"}
	if err := s.InsertReferenceCase(c); err != nil || c.ID == 0 {
		t.Fatalf("insert: %+v %v", c, err)
	}
	got, err := s.GetReferenceCase(c.ID)
	if err != nil || got == nil || len(got.GateCmds) != 2 || got.GateCmds[1] != "go test ./..." ||
		got.Tokens != 1234 || got.Model != "opus" || got.InvalidReason != "" {
		t.Fatalf("get = %+v %v", got, err)
	}
	if ok, _ := s.HasReferenceCase("p", "T.md:10", "abc"); !ok {
		t.Error("HasReferenceCase must see the stored case")
	}
	if ok, _ := s.HasReferenceCase("p", "T.md:10", "other"); ok {
		t.Error("another base commit is another case")
	}
	if other, _ := s.GetReferenceCase(999); other != nil {
		t.Errorf("missing case = %+v, want nil", other)
	}

	if err := s.SetReferenceCaseInvalid(c.ID, "base commit gone"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListReferenceCases("p"); len(list) != 1 || list[0].InvalidReason != "base commit gone" {
		t.Errorf("list = %+v", list)
	}
	if list, _ := s.ListReferenceCases("q"); len(list) != 0 {
		t.Errorf("another project's cases leaked: %+v", list)
	}

	r := &ReferenceReplay{CaseID: c.ID, Model: "sonnet", Effort: "medium", Passed: true, Rounds: 2, Tokens: 900, DurationSec: 60, Note: "ok"}
	if err := s.InsertReferenceReplay(r); err != nil || r.ID == 0 {
		t.Fatalf("insert replay: %+v %v", r, err)
	}
	reps, err := s.ListReferenceReplays(c.ID)
	if err != nil || len(reps) != 1 || !reps[0].Passed || reps[0].Rounds != 2 || reps[0].Model != "sonnet" {
		t.Fatalf("replays = %+v %v", reps, err)
	}
	if reps, _ := s.ListReferenceReplays(c.ID + 1); len(reps) != 0 {
		t.Errorf("replays of another case leaked: %+v", reps)
	}
}
