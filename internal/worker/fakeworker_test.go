package worker

// Integration tests of Client against the fakeworker scenario server
// (MIXED-TASKS.md MP-07). Each mandatory scenario from
// testdata/worker-scenarios/ is played end-to-end: this validates both the
// client behaviour (backoff, continuation, cap) and the scenario files that
// the control-plane e2e tests reuse.

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/testkit"
)

// greetSeed matches the seed_files content of the mixed e2e scenarios: every
// worker scenario patches `return "TODO"` inside this file.
const greetSeed = "package greet\n\n// Greet returns a greeting for name.\nfunc Greet(name string) string {\n\treturn \"TODO\"\n}\n"

func loadWorkerScenario(t *testing.T, name string) *testkit.WorkerScenario {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "worker-scenarios", name)
	sc, err := testkit.LoadWorkerScenario(path)
	if err != nil {
		t.Fatalf("load scenario %s: %v", name, err)
	}
	return sc
}

// newScenarioClient starts a fakeworker for the named scenario and returns a
// Client pointed at it with sleeps stubbed out (counted, not slept).
func newScenarioClient(t *testing.T, scenarioName string) (*Client, *testkit.FakeWorker, *int) {
	t.Helper()
	fw := testkit.NewFakeWorker(loadWorkerScenario(t, scenarioName))
	srv := httptest.NewServer(fw)
	t.Cleanup(srv.Close)

	cfg := config.WorkerConfig{
		Name: "fake", BaseURL: srv.URL, Model: "fake/model", KeyEnv: "FAKEWORKER_KEY",
		ContinuationCap: 3, RequestTimeoutSec: 30,
	}
	c := NewClient(cfg, "test-key")
	sleeps := 0
	c.sleep = func(ctx context.Context, d time.Duration) bool {
		sleeps++
		return true
	}
	return c, fw, &sleeps
}

func briefMessages() []ChatMessage {
	return []ChatMessage{
		{Role: "system", Content: DefaultBriefSystemPrompt},
		{Role: "user", Content: "Implement Greet."},
	}
}

func TestScenario_CleanRound1(t *testing.T) {
	c, fw, _ := newScenarioClient(t, "clean-round1.json")

	res, err := c.Complete(context.Background(), "t1", briefMessages(), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	parsed, err := ParsePatches(res.Content)
	if err != nil {
		t.Fatalf("ParsePatches: %v", err)
	}
	if len(parsed.Patches) != 1 || len(parsed.Warnings) != 0 {
		t.Fatalf("patches=%d warnings=%v", len(parsed.Patches), parsed.Warnings)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "greet.go"), []byte(greetSeed), 0o644); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyPatches(dir, parsed.Patches)
	if err != nil {
		t.Fatalf("ApplyPatches: %v", err)
	}
	if len(applied.Applied) != 1 || len(applied.Rejected) != 0 {
		t.Fatalf("applied=%d rejected=%+v", len(applied.Applied), applied.Rejected)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "greet.go"))
	if !strings.Contains(string(got), `return "Hello, " + name`) {
		t.Fatalf("patched file:\n%s", got)
	}
	if n := len(fw.Requests()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestScenario_BrokenThenClean(t *testing.T) {
	c, _, _ := newScenarioClient(t, "broken-then-clean.json")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "greet.go"), []byte(greetSeed), 0o644); err != nil {
		t.Fatal(err)
	}

	// Round 1: the anchor drops the `return` line (step37's known failure) —
	// the patch parses but must be rejected with a verbatim-diff reason.
	msgs := briefMessages()
	res1, err := c.Complete(context.Background(), "t1", msgs, nil)
	if err != nil {
		t.Fatalf("round 1 Complete: %v", err)
	}
	parsed1, err := ParsePatches(res1.Content)
	if err != nil {
		t.Fatalf("round 1 ParsePatches: %v", err)
	}
	applied1, err := ApplyPatches(dir, parsed1.Patches)
	if err != nil {
		t.Fatalf("round 1 ApplyPatches: %v", err)
	}
	if len(applied1.Rejected) != 1 || len(applied1.Applied) != 0 {
		t.Fatalf("round 1: applied=%d rejected=%d", len(applied1.Applied), len(applied1.Rejected))
	}
	if !strings.Contains(applied1.Rejected[0].Reason, "FIND not found verbatim") {
		t.Fatalf("rejection reason: %s", applied1.Rejected[0].Reason)
	}

	// Round 2 with the rejection feedback: the corrected patch applies clean.
	msgs = append(msgs,
		ChatMessage{Role: "assistant", Content: res1.Content},
		ChatMessage{Role: "user", Content: applied1.Rejected[0].Reason},
	)
	res2, err := c.Complete(context.Background(), "t1", msgs, nil)
	if err != nil {
		t.Fatalf("round 2 Complete: %v", err)
	}
	parsed2, err := ParsePatches(res2.Content)
	if err != nil {
		t.Fatalf("round 2 ParsePatches: %v", err)
	}
	applied2, err := ApplyPatches(dir, parsed2.Patches)
	if err != nil {
		t.Fatalf("round 2 ApplyPatches: %v", err)
	}
	if len(applied2.Applied) != 1 || len(applied2.Rejected) != 0 {
		t.Fatalf("round 2: applied=%d rejected=%+v", len(applied2.Applied), applied2.Rejected)
	}
}

func TestScenario_RateLimitStorm(t *testing.T) {
	c, fw, sleeps := newScenarioClient(t, "rate-limit-storm.json")

	res, err := c.Complete(context.Background(), "t1", briefMessages(), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := ParsePatches(res.Content); err != nil {
		t.Fatalf("ParsePatches after storm: %v", err)
	}
	// 3×429 then success: one request per attempt, one backoff sleep per 429.
	if n := len(fw.Requests()); n != 4 {
		t.Errorf("requests = %d, want 4", n)
	}
	if *sleeps != 3 {
		t.Errorf("backoff sleeps = %d, want 3", *sleeps)
	}
}

func TestScenario_LengthContinuation(t *testing.T) {
	c, fw, _ := newScenarioClient(t, "length-continuation.json")

	res, err := c.Complete(context.Background(), "t1", briefMessages(), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Continuations != 2 || res.FinishReason != "stop" {
		t.Fatalf("continuations=%d finish=%q", res.Continuations, res.FinishReason)
	}
	parsed, err := ParsePatches(res.Content)
	if err != nil {
		t.Fatalf("reassembled content does not parse: %v\n%s", err, res.Content)
	}
	if len(parsed.Patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(parsed.Patches))
	}

	reqs := fw.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	// Every continuation request must end with the exact continue prompt.
	for _, r := range reqs[1:] {
		last := r.Messages[len(r.Messages)-1]
		if last.Role != "user" || last.Content != "continue exactly where you stopped" {
			t.Fatalf("continuation request last message = %+v", last)
		}
	}
}

func TestScenario_LengthLoop_CapStopsContinuation(t *testing.T) {
	c, fw, _ := newScenarioClient(t, "length-loop.json")

	res, err := c.Complete(context.Background(), "t1", briefMessages(), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Continuations != 3 {
		t.Errorf("continuations = %d, want cap 3", res.Continuations)
	}
	if res.FinishReason != "length" {
		t.Errorf("finish = %q, want length (cap hit mid-output)", res.FinishReason)
	}
	// Initial request + 3 continuations, then the cap stops the loop even
	// though the scenario would keep answering finish_reason=length.
	if n := len(fw.Requests()); n != 4 {
		t.Errorf("requests = %d, want 4", n)
	}
}

func TestScenario_MissingEnd_Spliced(t *testing.T) {
	c, _, _ := newScenarioClient(t, "missing-end.json")

	res, err := c.Complete(context.Background(), "t1", briefMessages(), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	parsed, err := ParsePatches(res.Content)
	if err != nil {
		t.Fatalf("ParsePatches must splice, got error: %v", err)
	}
	if len(parsed.Patches) != 2 {
		t.Fatalf("patches = %d, want 2 (spliced)", len(parsed.Patches))
	}
	if len(parsed.Warnings) == 0 || !strings.Contains(parsed.Warnings[0], ">>>END") {
		t.Fatalf("expected a missing->>>END warning, got %v", parsed.Warnings)
	}
}
