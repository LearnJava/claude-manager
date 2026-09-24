package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"claude-manager/internal/config"
)

// parseHermesFile runs a real `hermes chat --format stream-json` recording
// (testdata/hermes-stream/) through hermesStream.
func parseHermesFile(t *testing.T, name string) []ParsedEvent {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "hermes-stream", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	h := newHermesStream()
	var out []ParsedEvent
	for _, line := range strings.Split(string(data), "\n") {
		out = append(out, h.Parse(line)...)
	}
	return append(out, h.Flush()...)
}

func TestHermesStream_ToolCallRecording(t *testing.T) {
	evs := parseHermesFile(t, "tool-call.jsonl")

	if evs[0].EventType != EventInit || evs[0].Init.SessionID != "20260924_111123_993e42" ||
		evs[0].Init.Model != "claude-haiku-4-5" {
		t.Fatalf("first event = %+v, want init with session id + model", evs[0])
	}

	var levels []string
	for _, ev := range evs {
		for _, e := range ev.Entries {
			levels = append(levels, e.Level)
		}
	}
	want := []string{"system", "tool", "tool_result", "text", "result"}
	if !slices.Equal(levels, want) {
		t.Fatalf("log levels = %v, want %v", levels, want)
	}
	tool := evs[1].Entries[0]
	if tool.ToolName != "terminal" || tool.ToolInput != "echo hello-cm" {
		t.Errorf("tool entry = %+v, want terminal: echo hello-cm", tool)
	}
	// Streaming deltas "\n\ndone" + "." are coalesced into ONE text entry.
	if got := evs[3].Entries[0].Message; got != "done." {
		t.Errorf("coalesced text = %q, want %q", got, "done.")
	}

	usage := evs[len(evs)-2].Usage
	if usage == nil || usage.InputTokens != 8 || usage.OutputTokens != 63 ||
		usage.CacheReadInputTokens != 14910 || usage.CacheCreationInputTokens != 14998 {
		t.Fatalf("usage = %+v, want the result's token split", usage)
	}
	res := evs[len(evs)-1]
	if res.EventType != EventResult || res.Result.ResultText != "done." ||
		res.Result.DurationMs != 7173 || res.Result.StopReason == "error" {
		t.Fatalf("result = %+v", res.Result)
	}
}

func TestHermesStream_TodoListFeedsTaskPanel(t *testing.T) {
	evs := parseHermesFile(t, "todo.jsonl")
	var todos []TodoItem
	for _, ev := range evs {
		if ev.Todos != nil {
			todos = ev.Todos
		}
	}
	if len(todos) != 2 || todos[0].Content != "read" || todos[1].Status != "completed" {
		t.Fatalf("todos = %+v, want the 2-item list from todo_list's result", todos)
	}
}

func TestHermesStream_FailedResult(t *testing.T) {
	h := newHermesStream()
	evs := h.Parse(`{"type":"result","session_id":"x","exit_code":1,"text":"","error":"HTTP 429: rate limit exceeded","tokens":{"input":0,"output":0}}`)
	res := evs[len(evs)-1]
	if res.EventType != EventResult || res.Result.StopReason != "error" {
		t.Fatalf("result = %+v, want StopReason=error", res.Result)
	}
	if h.lastError != "HTTP 429: rate limit exceeded" {
		t.Errorf("lastError = %q", h.lastError)
	}
	if _, ok := detectRateLimitText(h.lastError); !ok {
		t.Errorf("a 429 error text must be classified as a rate limit")
	}
}

func TestHermesStream_NonJSONLineSurfaces(t *testing.T) {
	evs := newHermesStream().Parse("Traceback (most recent call last):")
	if len(evs) != 1 || evs[0].Entries[0].Message != "Traceback (most recent call last):" {
		t.Fatalf("got %+v, want the raw line as a system entry", evs)
	}
}

func TestBuildHermesArgs(t *testing.T) {
	s := New(Params{ID: "p/S", Config: config.SessionConfig{
		Name: "S", Runtime: "hermes", Model: "anthropic/claude-sonnet-4.6",
		Effort: "high", PermissionMode: "bypassPermissions",
		HermesProvider: "openrouter", HermesProfile: "cm-p",
		HermesSkills: []string{"a", " ", "b"},
	}})
	got := s.buildHermesArgs("20260924_1", "")
	want := []string{
		"-p", "cm-p", "chat", "--query-file", "-", "--format", "stream-json", "--source", "tool",
		"--resume", "20260924_1", "-m", "anthropic/claude-sonnet-4.6", "--provider", "openrouter",
		"--reasoning", "high", "-s", "a", "-s", "b", "--yolo",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args =\n%v\nwant\n%v", got, want)
	}

	// A fresh conversation has no --resume; a non-bypass mode never gets
	// --yolo; an effort Hermes doesn't know is dropped, not passed through.
	s2 := New(Params{ID: "p/T", Config: config.SessionConfig{
		Name: "T", Runtime: "hermes", Model: "m", Effort: "turbo", PermissionMode: "acceptEdits",
	}})
	got2 := s2.buildHermesArgs("", "")
	for _, bad := range []string{"--resume", "--yolo", "--reasoning", "-p"} {
		if slices.Contains(got2, bad) {
			t.Errorf("args %v must not contain %s", got2, bad)
		}
	}
}

func TestDecodeInputLine_RoundTripsSendMessage(t *testing.T) {
	mustJSON := func(t *testing.T, m InputMessage) []byte {
		t.Helper()
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	text, imgs := decodeInputLine(mustJSON(t, userMessage("привет $(x) `y`")))
	if text != "привет $(x) `y`" || imgs != nil {
		t.Fatalf("text=%q imgs=%v", text, imgs)
	}
	text, imgs = decodeInputLine(mustJSON(t, userMessageWithImages("look",
		[]ImageAttachment{{MediaType: "image/png", DataBase64: "aGk="}})))
	if text != "look" || len(imgs) != 1 || imgs[0].DataBase64 != "aGk=" {
		t.Fatalf("text=%q imgs=%+v", text, imgs)
	}
}
