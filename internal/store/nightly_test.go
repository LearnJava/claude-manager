package store

import "testing"

func TestNightlyRun(t *testing.T) {
	s := newTestStore(t)
	if r, err := s.LastNightlyRun("p"); err != nil || r != nil {
		t.Fatalf("empty: %v %v", r, err)
	}
	if err := s.SetNightlyRun(NightlyRun{Project: "p", Passed: false, Note: "SUGGESTED-TASKS.md:7"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNightlyRun(NightlyRun{Project: "p", Passed: true}); err != nil {
		t.Fatal(err)
	}
	r, err := s.LastNightlyRun("p")
	if err != nil || r == nil || !r.Passed || r.Note != "" || r.TS.IsZero() {
		t.Fatalf("got %+v, %v", r, err)
	}
	if r, _ := s.LastNightlyRun("other"); r != nil {
		t.Fatal("another project's run leaked")
	}
}
