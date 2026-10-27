package store

import (
	"testing"
	"time"
)

func TestHookRuleCRUDAndUnique(t *testing.T) {
	s := newTestStore(t)
	r := &HookRule{Project: "p", Kind: "failure_cmd", Param: "go test ./...", Hint: "use: go test ./internal/..."}
	if err := s.InsertHookRule(r); err != nil || r.ID == 0 || r.Status != HookRuleDraft || r.Origin != "manual" {
		t.Fatalf("insert: %+v %v", r, err)
	}
	if err := s.InsertHookRule(&HookRule{Project: "p", Kind: "failure_cmd", Param: "go test ./..."}); err == nil {
		t.Error("the same (project, kind, param) must not be insertable twice")
	}
	if err := s.InsertHookRule(&HookRule{Project: "q", Kind: "failure_cmd", Param: "go test ./..."}); err != nil {
		t.Errorf("another project may hold the same rule: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateHookRule(r.ID, HookRuleTrial, &now, `{"code":"x"}`); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetHookRule(r.ID)
	if err != nil || got == nil || got.Status != HookRuleTrial || got.DecidedAt == nil || got.Reason != `{"code":"x"}` || got.Hint != r.Hint {
		t.Fatalf("get: %+v %v", got, err)
	}
	if missing, err := s.GetHookRule(9999); err != nil || missing != nil {
		t.Errorf("missing rule = %+v, %v; want nil, nil", missing, err)
	}
	if list, _ := s.ListHookRules("p"); len(list) != 1 {
		t.Errorf("ListHookRules(p) = %d rows, want 1", len(list))
	}
}
