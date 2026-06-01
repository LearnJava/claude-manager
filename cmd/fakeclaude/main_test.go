package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// scenarioPath returns the absolute-ish path to a testdata scenario file.
// Tests run with cwd = cmd/fakeclaude, so go up two levels.
func scenarioPath(name string) string {
	return "../../testdata/scenarios/" + name
}

// parseEvents splits buf by newlines and unmarshals each non-empty line into a map.
func parseEvents(t *testing.T, buf string) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	for _, line := range strings.Split(buf, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Logf("warning: non-JSON line: %q", line)
			continue
		}
		out = append(out, m)
	}
	return out
}

func TestHappyPath(t *testing.T) {
	t.Setenv("FAKECLAUDE_SPEED", "1000")

	pipeR, pipeW := io.Pipe()
	var buf bytes.Buffer

	path := scenarioPath("happy-path.json")

	// Write stdin in a goroutine to avoid deadlock.
	go func() {
		defer pipeW.Close()
		pipeW.Write([]byte(`{"type":"user_message","message":"hello simple task"}` + "\n"))
	}()

	code := run([]string{"--session-id", "test-sid", "--model", "claude-sonnet-4-6"}, pipeR, &buf, path)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	events := parseEvents(t, buf.String())
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d: %v", len(events), events)
	}

	wantTypes := []string{"system", "assistant", "result"}
	for i, want := range wantTypes {
		if i >= len(events) {
			t.Errorf("missing event[%d] (want type=%q)", i, want)
			continue
		}
		got, _ := events[i]["type"].(string)
		if got != want {
			t.Errorf("event[%d] type = %q, want %q", i, got, want)
		}
	}
}

func TestPermissionAllow(t *testing.T) {
	t.Setenv("FAKECLAUDE_SPEED", "1000")

	pipeR, pipeW := io.Pipe()
	var buf bytes.Buffer

	path := scenarioPath("permission-allow.json")

	go func() {
		defer pipeW.Close()
		pipeW.Write([]byte(`{"type":"user_message","message":"please edit the file"}` + "\n"))
		pipeW.Write([]byte(`{"type":"permission_response","request_id":"p1","decision":"allow"}` + "\n"))
	}()

	code := run([]string{"--session-id", "test-sid"}, pipeR, &buf, path)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	events := parseEvents(t, buf.String())

	// Expect: system(init), assistant, permission_request, assistant, result(success)
	wantSequence := []struct {
		typ     string
		subtype string
	}{
		{"system", "init"},
		{"assistant", ""},
		{"permission_request", ""},
		{"assistant", ""},
		{"result", "success"},
	}

	if len(events) < len(wantSequence) {
		t.Fatalf("expected at least %d events, got %d", len(wantSequence), len(events))
	}

	for i, want := range wantSequence {
		got, _ := events[i]["type"].(string)
		if got != want.typ {
			t.Errorf("event[%d] type = %q, want %q", i, got, want.typ)
		}
		if want.subtype != "" {
			gotSub, _ := events[i]["subtype"].(string)
			if gotSub != want.subtype {
				t.Errorf("event[%d] subtype = %q, want %q", i, gotSub, want.subtype)
			}
		}
	}
}

func TestPermissionDeny(t *testing.T) {
	t.Setenv("FAKECLAUDE_SPEED", "1000")

	pipeR, pipeW := io.Pipe()
	var buf bytes.Buffer

	path := scenarioPath("permission-deny.json")

	go func() {
		defer pipeW.Close()
		pipeW.Write([]byte(`{"type":"user_message","message":"delete the build artifacts"}` + "\n"))
		pipeW.Write([]byte(`{"type":"permission_response","request_id":"p2","decision":"deny"}` + "\n"))
	}()

	code := run([]string{"--session-id", "test-sid"}, pipeR, &buf, path)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (permission-deny exit_code is 0)", code)
	}

	events := parseEvents(t, buf.String())

	// Expect: system(init), assistant, permission_request, result(error)
	if len(events) < 4 {
		t.Fatalf("expected at least 4 events, got %d", len(events))
	}

	types := make([]string, len(events))
	for i, e := range events {
		types[i], _ = e["type"].(string)
	}

	// Check system init
	if types[0] != "system" {
		t.Errorf("event[0] type = %q, want system", types[0])
	}
	// Check assistant
	if types[1] != "assistant" {
		t.Errorf("event[1] type = %q, want assistant", types[1])
	}
	// Check permission_request
	if types[2] != "permission_request" {
		t.Errorf("event[2] type = %q, want permission_request", types[2])
	}
	// Last event should be result with subtype=error
	last := events[len(events)-1]
	if last["type"] != "result" {
		t.Errorf("last event type = %q, want result", last["type"])
	}
	if last["subtype"] != "error" {
		t.Errorf("last event subtype = %q, want error", last["subtype"])
	}
}

func TestErrorExit(t *testing.T) {
	t.Setenv("FAKECLAUDE_SPEED", "1000")

	pipeR, pipeW := io.Pipe()
	var buf bytes.Buffer

	path := scenarioPath("error-exit.json")

	go func() {
		defer pipeW.Close()
		pipeW.Write([]byte(`{"type":"user_message","message":"this will crash and fail"}` + "\n"))
	}()

	code := run([]string{"--session-id", "test-sid"}, pipeR, &buf, path)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}

	events := parseEvents(t, buf.String())
	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}

	// Find the result event
	var resultEvent map[string]interface{}
	for _, e := range events {
		if e["type"] == "result" {
			resultEvent = e
			break
		}
	}
	if resultEvent == nil {
		t.Fatal("no result event found")
	}
	if resultEvent["subtype"] != "error" {
		t.Errorf("result subtype = %q, want error", resultEvent["subtype"])
	}
}
