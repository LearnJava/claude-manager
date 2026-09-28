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
	entryAt := func(level string) config.LogEntry {
		for _, ev := range evs {
			for _, e := range ev.Entries {
				if e.Level == level {
					return e
				}
			}
		}
		t.Fatalf("no %s entry", level)
		return config.LogEntry{}
	}
	tool := entryAt("tool")
	if tool.ToolName != "terminal" || tool.ToolInput != "echo hello-cm" {
		t.Errorf("tool entry = %+v, want terminal: echo hello-cm", tool)
	}
	// Streaming deltas "\n\ndone" + "." are coalesced into ONE text entry.
	if got := entryAt("text").Message; got != "done." {
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

// TestHermesStream_ToolUseIDLinksCallAndResult: Hermes has no wire-level
// tool_use_id, so hermesStream synthesizes one — two different tools' calls
// and results (interleaved) must still pair up correctly.
func TestHermesStream_ToolUseIDLinksCallAndResult(t *testing.T) {
	h := newHermesStream()
	var evs []ParsedEvent
	for _, line := range []string{
		`{"type":"tool_use","name":"terminal","input":{"command":"ls"}}`,
		`{"type":"tool_use","name":"read_file","input":{"path":"a.go"}}`,
		`{"type":"tool_result","name":"read_file","output":"package a","is_error":false}`,
		`{"type":"tool_result","name":"terminal","output":"boom","is_error":true}`,
	} {
		evs = append(evs, h.Parse(line)...)
	}
	if len(evs) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(evs), evs)
	}
	terminalCallID := evs[0].Entries[0].ToolUseID
	readCallID := evs[1].Entries[0].ToolUseID
	if terminalCallID == "" || readCallID == "" || terminalCallID == readCallID {
		t.Fatalf("expected distinct non-empty ids, got %q/%q", terminalCallID, readCallID)
	}
	if evs[2].Entries[0].ToolUseID != readCallID {
		t.Errorf("read_file result should link to its own call, got %q want %q",
			evs[2].Entries[0].ToolUseID, readCallID)
	}
	if evs[3].Entries[0].ToolUseID != terminalCallID || evs[3].Entries[0].Level != "error" {
		t.Errorf("terminal result should link to its own call and be an error, got %+v", evs[3].Entries[0])
	}
}

// TestHermesStream_PatchToolUseCarriesDiff: hermes' patch tool_use input maps
// to the same FileDiff shape the Claude Edit path produces (VIEW-TASKS.md
// UI-04's "Hermes-тест на patch").
func TestHermesStream_PatchToolUseCarriesDiff(t *testing.T) {
	h := newHermesStream()
	evs := h.Parse(`{"type":"tool_use","name":"patch","input":{"path":"a.go","old_string":"foo","new_string":"bar\nbaz"}}`)
	if len(evs) != 1 || len(evs[0].Entries) != 1 {
		t.Fatalf("got %+v", evs)
	}
	diff := evs[0].Entries[0].Diff
	if diff == nil {
		t.Fatal("expected a Diff on the patch tool_use entry, got nil")
	}
	if diff.Added != 2 || diff.Removed != 1 {
		t.Fatalf("+%d -%d, want +2 -1", diff.Added, diff.Removed)
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
		"--resume", "20260924_1", "--max-turns", "500", "-m", "anthropic/claude-sonnet-4.6", "--provider", "openrouter",
		"--reasoning", "high", "-s", "a", "-s", "b", "--yolo",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args =\n%v\nwant\n%v", got, want)
	}

	// hermes_max_turns overrides the manager's default step budget.
	sMax := New(Params{ID: "p/M", Config: config.SessionConfig{Name: "M", Runtime: "hermes", HermesMaxTurns: 80}})
	if v := argValue(sMax.buildHermesArgs("", ""), "--max-turns"); v != "80" {
		t.Errorf("--max-turns = %q, want 80", v)
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

	// Claude CLI aliases are ambiguous to Hermes (HTTP 404: model: opus) and
	// must be passed as exact ids.
	s3 := New(Params{ID: "p/U", Config: config.SessionConfig{Name: "U", Runtime: "hermes", Model: "opus"}})
	if m := argValue(s3.buildHermesArgs("", ""), "-m"); m != "claude-opus-5-5" {
		t.Errorf("-m for alias opus = %q, want claude-opus-5-5", m)
	}
}

func TestHermesEnv_PinsProjectCwdAndDropsParentSession(t *testing.T) {
	parent := []string{
		"PATH=C:\\bin",
		"TERMINAL_CWD=C:\\Users\\konstantin",
		"HERMES_HOME=C:\\h",
		"HERMES_GIT_BASH_PATH=C:\\git\\bash.exe",
		"HERMES_SESSION_ID=20260924_102935_377658",
		"HERMES_MAX_ITERATIONS=150",
		"Hermes_Desktop=1",
		"PYTHONUTF8=0",
	}
	env := hermesEnv(parent, `C:\proj`)
	has := func(kv string) bool { return slices.Contains(env, kv) }
	for _, want := range []string{"PATH=C:\\bin", "TERMINAL_CWD=C:\\proj", "HERMES_HOME=C:\\h",
		"HERMES_GIT_BASH_PATH=C:\\git\\bash.exe", "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8"} {
		if !has(want) {
			t.Errorf("env missing %s: %v", want, env)
		}
	}
	for _, bad := range []string{"TERMINAL_CWD=C:\\Users\\konstantin", "HERMES_SESSION_ID=20260924_102935_377658",
		"HERMES_MAX_ITERATIONS=150", "Hermes_Desktop=1", "PYTHONUTF8=0"} {
		if has(bad) {
			t.Errorf("env must not carry %s", bad)
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

// TestHermesStream_ToolCallIDAndDuration: tool_call_id from the wire wins over
// the synthetic hermes-N, and duration_ms reaches the result entry (UI-08).
func TestHermesStream_ToolCallIDAndDuration(t *testing.T) {
	h := newHermesStream()
	var evs []ParsedEvent
	for _, line := range []string{
		`{"type":"tool_use","name":"terminal","tool_call_id":"call_1","input":{"command":"ls"}}`,
		`{"type":"tool_result","name":"terminal","tool_call_id":"call_1","output":"ok","duration_ms":312}`,
	} {
		evs = append(evs, h.Parse(line)...)
	}
	call, res := evs[0].Entries[0], evs[1].Entries[0]
	if call.ToolUseID != "call_1" || res.ToolUseID != "call_1" {
		t.Errorf("ids = %q / %q, want call_1", call.ToolUseID, res.ToolUseID)
	}
	if res.DurationMs != 312 {
		t.Errorf("DurationMs = %d, want 312", res.DurationMs)
	}
}

// A clarify call's questions ride on its tool_result, not its tool_use: by
// then Hermes has persisted the call and its headless answer.
func TestHermesStream_ClarifyQuestionsOnToolResult(t *testing.T) {
	h := newHermesStream()
	use := h.Parse(`{"type":"tool_use","name":"clarify","tool_call_id":"c1","input":{"questions":[` +
		`{"question":"Route?","choices":["a","b"]},{"question":"  "},{"question":"Why?","multi_select":true}]}}`)
	for _, ev := range use {
		if ev.Clarify != nil {
			t.Fatalf("tool_use must not carry the questions yet: %+v", ev)
		}
	}
	res := h.Parse(`{"type":"tool_result","name":"clarify","tool_call_id":"c1","output":"{}"}`)
	var got []ClarifyQuestion
	for _, ev := range res {
		got = append(got, ev.Clarify...)
	}
	want := []ClarifyQuestion{
		{Question: "Route?", Choices: []string{"a", "b"}},
		{Question: "Why?", MultiSelect: true},
	}
	if len(got) != len(want) {
		t.Fatalf("clarify = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Question != want[i].Question || !slices.Equal(got[i].Choices, want[i].Choices) ||
			got[i].MultiSelect != want[i].MultiSelect {
			t.Errorf("clarify[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	// A second result for the same id carries nothing: the questions were consumed.
	for _, ev := range h.Parse(`{"type":"tool_result","name":"clarify","tool_call_id":"c1","output":"{}"}`) {
		if ev.Clarify != nil {
			t.Errorf("questions delivered twice: %+v", ev.Clarify)
		}
	}
}

func TestParseClarifyInput(t *testing.T) {
	legacy := parseClarifyInput(json.RawMessage(`{"question":"Go?","choices":["yes","no"]}`))
	if len(legacy) != 1 || legacy[0].Question != "Go?" || !slices.Equal(legacy[0].Choices, []string{"yes", "no"}) {
		t.Errorf("legacy form = %+v", legacy)
	}
	for _, in := range []string{``, `not json`, `{}`, `{"questions":[{"question":""}]}`} {
		if qs := parseClarifyInput(json.RawMessage(in)); qs != nil {
			t.Errorf("parseClarifyInput(%q) = %+v, want nil", in, qs)
		}
	}
}

func TestClarifyAnswerMessage(t *testing.T) {
	msg := clarifyAnswerMessage(&clarifyState{
		questions: []ClarifyQuestion{{Question: "Route?"}, {Question: "Screen free?"}},
		answers:   []string{"direct", "yes"},
	})
	for _, want := range []string{"disregard", "1. Route?\n   Answer: direct", "2. Screen free?\n   Answer: yes"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if strings.HasSuffix(msg, "\n") {
		t.Errorf("message has a trailing newline: %q", msg)
	}
}
