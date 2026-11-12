package session

import (
	"encoding/json"
	"slices"
	"testing"

	"claude-manager/internal/config"
)

// Real wire lines recorded from opencode v2.2.7 (`opencode run --format
// json`) — see opencode_parser.go's doc comment.
const (
	ocLineInit   = `{"type":"step_start","timestamp":1794487349361,"sessionID":"sesCmtest1","part":{"id":"prt_1","sessionID":"sesCmtest1","messageID":"msg_1","type":"step-start"}}`
	ocLineText   = `{"type":"text","timestamp":1794487349817,"sessionID":"sesCmtest1","part":{"id":"prt_2","type":"text","text":"HELLO_FROM_OPENCODE","time":{"start":1794487349737,"end":1794487349817}}}`
	ocLineTool   = `{"type":"tool_use","timestamp":1794487367757,"sessionID":"sesCmtest2","part":{"partID":"prt_3","type":"tool","id":"call_bc10","tool":"shell","state":{"status":"completed","input":{"command":"echo OC_TOOL_TEST_OK"},"output":"OC_TOOL_TEST_OK\r\n"}}}`
	ocLineFinish = `{"type":"step_finish","timestamp":1794487367760,"sessionID":"sesCmtest2","part":{"id":"prt_4","type":"step-finish","reason":"tool-calls","cost":0,"tokens":{"input":7379,"output":15,"reasoning":15,"cache":{"read":64,"write":0}}}}`
	// The same step, one model call later: the final step_finish ends the turn.
	ocLineFinishStop = `{"type":"step_finish","timestamp":1794487369900,"sessionID":"sesCmtest2","part":{"id":"prt_5","type":"step-finish","reason":"stop","cost":0,"tokens":{"input":8110,"output":42,"reasoning":30,"cache":{"read":128,"write":0}}}}`
	ocLineErr    = `{"type":"error","timestamp":1794487620934,"sessionID":"sesCmbad","error":{"type":"provider.no-route","message":"Model unavailable: bogus/nope"}}`
)

func TestOpenCodeStream(t *testing.T) {
	t.Run("happy turn", func(t *testing.T) {
		o := newOpenCodeStream()
		var evs []ParsedEvent
		for _, l := range []string{ocLineInit, ocLineTool, ocLineText, ocLineFinish, ocLineFinishStop} {
			evs = append(evs, o.Parse(l)...)
		}
		evs = append(evs, o.Flush()...)

		var inits, results, usage int
		var toolEntries, resultEntries, textEntries int
		var gotSessionID string
		var gotUsage []TokenUsage
		for _, ev := range evs {
			switch ev.EventType {
			case EventInit:
				inits++
				gotSessionID = ev.Init.SessionID
			case EventResult:
				results++
				if ev.Result.StopReason != "stop" {
					t.Errorf("result stop reason = %q, want stop", ev.Result.StopReason)
				}
			case EventLog:
				for _, e := range ev.Entries {
					switch e.Level {
					case "tool":
						toolEntries++
						if e.ToolName != "shell" || e.ToolUseID != "call_bc10" {
							t.Errorf("tool entry = %q/%q, want shell/call_bc10", e.ToolName, e.ToolUseID)
						}
					case "tool_result":
						resultEntries++
						if e.ToolUseID != "call_bc10" {
							t.Errorf("tool_result ToolUseID = %q, want call_bc10 (paired with the call)", e.ToolUseID)
						}
						if e.Message != "OC_TOOL_TEST_OK\r\n" {
							t.Errorf("tool_result message = %q", e.Message)
						}
					case "text":
						textEntries++
						if e.Message != "HELLO_FROM_OPENCODE" {
							t.Errorf("text entry = %q", e.Message)
						}
					}
				}
			}
			if ev.Usage != nil {
				usage++
				gotUsage = append(gotUsage, *ev.Usage)
			}
		}
		wantUsage := []TokenUsage{
			{InputTokens: 7379, OutputTokens: 15, CacheReadInputTokens: 64},
			{InputTokens: 8110, OutputTokens: 42, CacheReadInputTokens: 128},
		}
		if !slices.EqualFunc(gotUsage, wantUsage, func(a, b TokenUsage) bool {
			return a.InputTokens == b.InputTokens && a.OutputTokens == b.OutputTokens &&
				a.CacheReadInputTokens == b.CacheReadInputTokens && a.CacheCreationInputTokens == b.CacheCreationInputTokens
		}) {
			t.Errorf("usage per step = %+v, want %+v (one per step_finish, intermediates included)", gotUsage, wantUsage)
		}
		if inits != 1 || gotSessionID != "sesCmtest1" {
			t.Errorf("inits = %d (session %q), want 1 (sesCmtest1)", inits, gotSessionID)
		}
		// One usage per step_finish (the intermediate one included), one
		// result only for the final "stop".
		if results != 1 || usage != 2 {
			t.Errorf("results = %d, usage events = %d, want 1/2", results, usage)
		}
		if toolEntries != 1 || resultEntries != 1 || textEntries != 1 {
			t.Errorf("entries tool/result/text = %d/%d/%d, want 1/1/1", toolEntries, resultEntries, textEntries)
		}
		if o.LastError() != "" {
			t.Errorf("LastError = %q, want empty", o.LastError())
		}
	})

	t.Run("intermediate finish is progress, not a turn end", func(t *testing.T) {
		o := newOpenCodeStream()
		var evs []ParsedEvent
		for _, l := range []string{ocLineInit, ocLineTool, ocLineFinish} {
			evs = append(evs, o.Parse(l)...)
		}
		var results, usage int
		turnDone := false
		for _, ev := range evs {
			if ev.EventType == EventResult {
				results++
			}
			if ev.Usage != nil {
				usage++
				if ev.Usage.InputTokens != 7379 {
					t.Errorf("usage = %+v, want the step's tokens", ev.Usage)
				}
			}
			for _, e := range ev.Entries {
				if e.Level == "result" && e.Message == "Turn completed" {
					turnDone = true
				}
			}
		}
		if results != 0 || usage != 1 || turnDone {
			t.Errorf("results = %d, usage = %d, turnDone entry = %v; want 0/1/false (progress must not render as the done badge)",
				results, usage, turnDone)
		}
	})

	t.Run("error line", func(t *testing.T) {
		o := newOpenCodeStream()
		var evs []ParsedEvent
		for _, l := range []string{ocLineInit, ocLineErr} {
			evs = append(evs, o.Parse(l)...)
		}
		var errLogs, results int
		resultIsError := false
		for _, ev := range evs {
			if ev.EventType == EventResult {
				results++
				resultIsError = ev.Result != nil && ev.Result.StopReason == "error"
			}
			for _, e := range ev.Entries {
				if e.Level == "error" {
					errLogs++
				}
			}
		}
		if errLogs != 1 || results != 1 || !resultIsError {
			t.Errorf("errLogs = %d, results = %d (isError=%v), want 1/1/true", errLogs, results, resultIsError)
		}
		if o.LastError() != "Model unavailable: bogus/nope" {
			t.Errorf("LastError = %q", o.LastError())
		}
	})

	t.Run("ask-user marker rides the result", func(t *testing.T) {
		o := newOpenCodeStream()
		inner := "pick one\n```ask-user\n" + `{"question": "Route?", "options": ["direct", "proxy"]}` + "\n```"
		textLine, err := json.Marshal(map[string]any{
			"type": "text", "timestamp": 1, "sessionID": "sesQ",
			"part": map[string]any{"type": "text", "text": inner}})
		if err != nil {
			t.Fatalf("marshal text line: %v", err)
		}
		finish := `{"type":"step_finish","timestamp":2,"sessionID":"sesQ","part":{"type":"step-finish","reason":"stop","cost":0,"tokens":{"input":1,"output":1,"cache":{"read":0,"write":0}}}}`
		var qs []Question
		for _, l := range []string{ocLineInit, string(textLine), finish} {
			for _, ev := range o.Parse(l) {
				qs = append(qs, ev.Questions...)
			}
		}
		if len(qs) != 1 || qs[0].Text != "Route?" || len(qs[0].Choices) != 2 {
			t.Errorf("questions = %+v, want one 'Route?' with 2 choices", qs)
		}
	})

	t.Run("non-protocol line surfaces verbatim", func(t *testing.T) {
		o := newOpenCodeStream()
		evs := o.Parse("a stray traceback line")
		if len(evs) != 1 || len(evs[0].Entries) != 1 || evs[0].Entries[0].Message != "a stray traceback line" {
			t.Errorf("evs = %+v", evs)
		}
	})
}

func TestBuildOpenCodeArgs(t *testing.T) {
	s := New(Params{
		ID: "p/oc", ProjectName: "p", ProjectPath: "D:\\proj",
		Config: config.SessionConfig{Name: "oc", Runtime: config.RuntimeOpenCode,
			Model: "zai-coding-plan/glm-5.3", Effort: "high"},
	})
	s.activeModel = "zai-coding-plan/glm-5.3"

	args := s.buildOpenCodeArgs("sesabc12345", "")
	want := []string{"run", "--format", "json", "--auto", "--archived",
		"--session", "sesabc12345",
		"--model", "zai-coding-plan/glm-5.3#high"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}

	// No effort: the selector stays bare. No conv id: no --session flag.
	s.Config.Effort = ""
	got := s.buildOpenCodeArgs("", "")
	if !slices.Equal(got, []string{"run", "--format", "json", "--auto", "--archived",
		"--model", "zai-coding-plan/glm-5.3"}) {
		t.Errorf("args = %v", got)
	}
}
