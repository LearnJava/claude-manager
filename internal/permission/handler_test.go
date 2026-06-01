package permission

import (
	"context"
	"errors"
	"testing"
	"time"

	"claude-manager/internal/config"
)

func TestNewPermissionHandlerNilDependencies(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	if h.RuntimeRules() == nil {
		t.Fatalf("runtimeRules should be auto-initialized when nil")
	}
	if h.Queue() == nil {
		t.Fatalf("queue should be auto-initialized when nil")
	}
	if h.ConfigRules() == nil || len(h.ConfigRules()) != 0 {
		t.Fatalf("config rules should be a non-nil empty slice when constructed from nil")
	}
}

func TestSetConfigRules(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	rules := []config.PermissionRule{{Tool: "Bash", Pattern: "ls", Decision: "allow"}}
	h.SetConfigRules(rules)
	if got := h.ConfigRules(); len(got) != 1 || got[0].Pattern != "ls" {
		t.Fatalf("SetConfigRules did not update: %+v", got)
	}
}

func TestConfigRulesReturnsCopy(t *testing.T) {
	h := NewPermissionHandler([]config.PermissionRule{{Tool: "Bash", Pattern: "ls", Decision: "allow"}}, nil, nil)
	got := h.ConfigRules()
	got[0].Pattern = "mutated"
	if h.ConfigRules()[0].Pattern == "mutated" {
		t.Fatalf("ConfigRules must return a copy")
	}
}

func TestCheckAutoConfigAllow(t *testing.T) {
	h := NewPermissionHandler([]config.PermissionRule{
		{Tool: "Bash", Pattern: "ls", Decision: "allow"},
	}, nil, nil)
	dec, src, matched, ok := h.CheckAuto(PermissionRequest{Tool: "Bash", Command: "ls"})
	if !ok || dec != "allow" || src != SourceConfigRule || matched == nil {
		t.Fatalf("CheckAuto = (%q,%q,%v,%v); want allow/config/matched/true", dec, src, matched, ok)
	}
}

func TestCheckAutoConfigDeny(t *testing.T) {
	h := NewPermissionHandler([]config.PermissionRule{
		{Tool: "Bash", Pattern: "rm *", Decision: "deny"},
	}, nil, nil)
	dec, src, _, ok := h.CheckAuto(PermissionRequest{Tool: "Bash", Command: "rm -rf /"})
	if !ok || dec != "deny" || src != SourceConfigRule {
		t.Fatalf("expected auto deny from config rule; got (%q,%q,%v)", dec, src, ok)
	}
}

func TestCheckAutoAskOverridesAllow(t *testing.T) {
	// "git push*" => ask must short-circuit and prevent the later wildcard allow.
	h := NewPermissionHandler([]config.PermissionRule{
		{Tool: "Bash", Pattern: "git push*", Decision: "ask"},
		{Tool: "Bash", Pattern: "*", Decision: "allow"},
	}, nil, nil)
	_, src, matched, ok := h.CheckAuto(PermissionRequest{Tool: "Bash", Command: "git push origin"})
	if ok {
		t.Fatalf("ask rule must not auto-resolve")
	}
	if matched == nil || matched.Pattern != "git push*" {
		t.Fatalf("expected ask rule to be reported as matched, got %+v", matched)
	}
	if src != SourceConfigRule {
		t.Fatalf("source = %q, want %q", src, SourceConfigRule)
	}
}

func TestCheckAutoRuntimeAllow(t *testing.T) {
	rt := NewRuntimeRuleSet()
	rt.Add("Bash", "cargo *", "allow")
	h := NewPermissionHandler(nil, rt, nil)
	dec, src, _, ok := h.CheckAuto(PermissionRequest{Tool: "Bash", Command: "cargo test"})
	if !ok || dec != "allow" || src != SourceRuntimeRule {
		t.Fatalf("expected runtime allow; got (%q,%q,%v)", dec, src, ok)
	}
}

func TestCheckAutoRuntimeAsk(t *testing.T) {
	rt := NewRuntimeRuleSet()
	rt.Add("Bash", "git push*", "ask")
	h := NewPermissionHandler(nil, rt, nil)
	_, src, matched, ok := h.CheckAuto(PermissionRequest{Tool: "Bash", Command: "git push origin"})
	if ok {
		t.Fatalf("runtime ask must not auto-resolve")
	}
	if matched == nil || src != SourceRuntimeRule {
		t.Fatalf("expected matched runtime ask; got src=%q matched=%+v", src, matched)
	}
}

func TestCheckAutoNoMatch(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	_, src, matched, ok := h.CheckAuto(PermissionRequest{Tool: "Bash", Command: "ls"})
	if ok {
		t.Fatalf("expected no match")
	}
	if src != "" || matched != nil {
		t.Fatalf("unexpected src=%q matched=%v", src, matched)
	}
}

func TestCheckAutoConfigBeatsRuntime(t *testing.T) {
	rt := NewRuntimeRuleSet()
	rt.Add("Bash", "ls", "deny")
	h := NewPermissionHandler([]config.PermissionRule{
		{Tool: "Bash", Pattern: "ls", Decision: "allow"},
	}, rt, nil)
	dec, src, _, ok := h.CheckAuto(PermissionRequest{Tool: "Bash", Command: "ls"})
	if !ok || dec != "allow" || src != SourceConfigRule {
		t.Fatalf("config rule must take precedence; got (%q,%q,%v)", dec, src, ok)
	}
}

func TestHandleAutoAllowSkipsChannel(t *testing.T) {
	h := NewPermissionHandler([]config.PermissionRule{
		{Tool: "Bash", Pattern: "ls", Decision: "allow"},
	}, nil, nil)
	req := PermissionRequest{ID: "r1", Tool: "Bash", Command: "ls"}
	res, err := h.Handle(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if res.CLIAnswer != "allow" || res.Source != SourceConfigRule {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Response.RequestID != "r1" {
		t.Fatalf("response request id mismatch: %+v", res.Response)
	}
	if h.Queue().Len() != 0 {
		t.Fatalf("auto-decided request should not enter the queue")
	}
}

func TestHandleAutoDenyFromRuntime(t *testing.T) {
	rt := NewRuntimeRuleSet()
	rt.Add("Bash", "rm *", "deny")
	h := NewPermissionHandler(nil, rt, nil)
	req := PermissionRequest{ID: "r2", Tool: "Bash", Command: "rm -rf /"}
	res, err := h.Handle(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if res.CLIAnswer != "deny" || res.Source != SourceRuntimeRule {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestHandleBlocksOnChannelAndNotifies(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	req := PermissionRequest{ID: "r3", Tool: "Bash", Command: "npm install"}

	notified := make(chan PermissionRequest, 1)
	respCh := make(chan PermissionResponse, 1)

	done := make(chan struct {
		res Result
		err error
	}, 1)
	go func() {
		res, err := h.Handle(context.Background(), req, respCh, func(r PermissionRequest) {
			notified <- r
		})
		done <- struct {
			res Result
			err error
		}{res, err}
	}()

	select {
	case got := <-notified:
		if got.ID != "r3" {
			t.Fatalf("notify got id=%q, want r3", got.ID)
		}
	case <-time.After(time.Second):
		t.Fatalf("notify was not called")
	}

	// Confirm queued while waiting.
	if h.Queue().Len() != 1 {
		t.Fatalf("queue should hold the pending request, got len=%d", h.Queue().Len())
	}

	respCh <- PermissionResponse{Decision: "allow"}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Handle err: %v", got.err)
		}
		if got.res.CLIAnswer != "allow" || got.res.Source != SourceUser {
			t.Fatalf("unexpected result: %+v", got.res)
		}
	case <-time.After(time.Second):
		t.Fatalf("Handle did not return after response")
	}

	if h.Queue().Len() != 0 {
		t.Fatalf("queue should be drained after Handle returns")
	}
}

func TestHandleContextCanceled(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	req := PermissionRequest{ID: "r4", Tool: "Bash", Command: "rm"}
	respCh := make(chan PermissionResponse)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct {
		res Result
		err error
	}, 1)
	go func() {
		res, err := h.Handle(ctx, req, respCh, func(PermissionRequest) {})
		done <- struct {
			res Result
			err error
		}{res, err}
	}()

	// Let the goroutine reach the select.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", got.err)
		}
		if got.res.CLIAnswer != "deny" || got.res.Source != SourceCanceled {
			t.Fatalf("expected deny-on-cancel; got %+v", got.res)
		}
	case <-time.After(time.Second):
		t.Fatalf("Handle did not return after context cancel")
	}

	if h.Queue().Len() != 0 {
		t.Fatalf("queue should be drained after cancel")
	}
}

func TestHandleClosedChannelReturnsErr(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	req := PermissionRequest{ID: "r5", Tool: "Bash", Command: "ls"}
	respCh := make(chan PermissionResponse)
	close(respCh)

	_, err := h.Handle(context.Background(), req, respCh, func(PermissionRequest) {})
	if !errors.Is(err, ErrNoResponse) {
		t.Fatalf("err = %v, want ErrNoResponse", err)
	}
}

func TestHandleNilResponseChannel(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	req := PermissionRequest{ID: "r6", Tool: "Bash", Command: "ls"}
	_, err := h.Handle(context.Background(), req, nil, func(PermissionRequest) {})
	if err == nil {
		t.Fatalf("expected error when responseCh is nil and no auto-rule applies")
	}
}

func TestHandleAllowSessionRegistersRuntimeRule(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	req := PermissionRequest{ID: "r7", Tool: "Bash", Command: "npm install"}
	respCh := make(chan PermissionResponse, 1)
	respCh <- PermissionResponse{Decision: "allow_session"}

	res, err := h.Handle(context.Background(), req, respCh, func(PermissionRequest) {})
	if err != nil {
		t.Fatalf("Handle err: %v", err)
	}
	if res.AddedRule == nil {
		t.Fatalf("expected runtime rule to be added")
	}
	if res.AddedRule.Tool != "Bash" || res.AddedRule.Pattern != "npm install" || res.AddedRule.Decision != "allow" {
		t.Fatalf("unexpected added rule: %+v", res.AddedRule)
	}
	if res.Persist {
		t.Fatalf("allow_session must not request persistence")
	}
	if !h.RuntimeRules().Allows(req) {
		t.Fatalf("runtime rule set should now allow the request")
	}
}

func TestHandleAllowAlwaysSignalsPersist(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	req := PermissionRequest{ID: "r8", Tool: "Bash", Command: "cargo build"}
	respCh := make(chan PermissionResponse, 1)
	respCh <- PermissionResponse{Decision: "allow_always"}

	res, err := h.Handle(context.Background(), req, respCh, func(PermissionRequest) {})
	if err != nil {
		t.Fatalf("Handle err: %v", err)
	}
	if !res.Persist {
		t.Fatalf("allow_always must request persistence")
	}
	if res.AddedRule == nil || res.AddedRule.Decision != "allow" {
		t.Fatalf("expected runtime allow rule, got %+v", res.AddedRule)
	}
	if res.CLIAnswer != "allow" {
		t.Fatalf("CLIAnswer = %q, want allow", res.CLIAnswer)
	}
}

func TestHandleDenyAlwaysSignalsPersist(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	req := PermissionRequest{ID: "r9", Tool: "Bash", Command: "rm -rf /tmp"}
	respCh := make(chan PermissionResponse, 1)
	respCh <- PermissionResponse{Decision: "deny_always"}

	res, err := h.Handle(context.Background(), req, respCh, func(PermissionRequest) {})
	if err != nil {
		t.Fatalf("Handle err: %v", err)
	}
	if !res.Persist {
		t.Fatalf("deny_always must request persistence")
	}
	if res.AddedRule == nil || res.AddedRule.Decision != "deny" {
		t.Fatalf("expected deny rule, got %+v", res.AddedRule)
	}
	if res.CLIAnswer != "deny" {
		t.Fatalf("CLIAnswer = %q, want deny", res.CLIAnswer)
	}
}

func TestHandlePlainAllowDoesNotAddRule(t *testing.T) {
	h := NewPermissionHandler(nil, nil, nil)
	req := PermissionRequest{ID: "r10", Tool: "Bash", Command: "ls"}
	respCh := make(chan PermissionResponse, 1)
	respCh <- PermissionResponse{Decision: "allow"}

	res, err := h.Handle(context.Background(), req, respCh, func(PermissionRequest) {})
	if err != nil {
		t.Fatalf("Handle err: %v", err)
	}
	if res.AddedRule != nil {
		t.Fatalf("plain allow must not add a runtime rule, got %+v", res.AddedRule)
	}
	if res.Persist {
		t.Fatalf("plain allow must not persist")
	}
	if h.RuntimeRules().Len() != 0 {
		t.Fatalf("runtime set must remain empty after plain allow")
	}
}

func TestHandleResponseRequestIDOverwritten(t *testing.T) {
	// Callers may forget to set RequestID — Handle should populate it.
	h := NewPermissionHandler(nil, nil, nil)
	req := PermissionRequest{ID: "r11", Tool: "Bash", Command: "ls"}
	respCh := make(chan PermissionResponse, 1)
	respCh <- PermissionResponse{Decision: "allow"} // no RequestID

	res, err := h.Handle(context.Background(), req, respCh, func(PermissionRequest) {})
	if err != nil {
		t.Fatalf("Handle err: %v", err)
	}
	if res.Response.RequestID != "r11" {
		t.Fatalf("RequestID = %q, want r11", res.Response.RequestID)
	}
}
