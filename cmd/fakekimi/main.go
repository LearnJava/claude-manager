// fakekimi is a scripted Kimi Code CLI double for tests: it parses the same
// argv shape as the real `kimi -p <prompt> --output-format stream-json
// [--session id] [-m model]` and emits deterministic stream-json, selected by
// substrings in the prompt (the fakehermes convention). No API tokens, no
// real Kimi binary. Scenarios:
//
//   - default: init, an "echo: <prompt>" assistant text, a result with usage.
//   - prompt contains "tool": a tool_use/tool_result pair sharing one id.
//   - "ask-user-marker": a result carrying an ```ask-user fenced question;
//     the next turn's reply contains "proceeding with the chosen budget".
//   - "resume-demo turn 2": reply contains "step 2".
//   - FAIL_TRANSIENT_ONCE: fail once with a timeout, then succeed on --session;
//     FAKEKIMI_STATE_FILE records the recovered conversation id.
//   - FAIL_429 / FAIL_403 / FAIL_SESSION_NOT_FOUND / FAIL_DESKTOP: the
//     matching stderr line and exit code 1.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func emit(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "acp" {
		fakeACP()
		return
	}
	var prompt, convID, model string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p":
			if i+1 < len(args) {
				i++
				prompt = args[i]
			}
		case "--session":
			if i+1 < len(args) {
				i++
				convID = args[i]
			}
		case "-m":
			if i+1 < len(args) {
				i++
				model = args[i]
			}
		}
	}
	id := convID
	if id == "" {
		id = "kimi-fake-session-1"
	}

	fail := func(line string) {
		fmt.Fprintln(os.Stderr, line)
		os.Exit(1)
	}
	switch {
	case strings.Contains(prompt, "FAIL_TRANSIENT_ONCE"):
		state := os.Getenv("FAKEKIMI_STATE_FILE")
		if state != "" {
			if _, err := os.Stat(state); os.IsNotExist(err) {
				id = "kimi-fake-transient-id"
				emit(map[string]any{"type": "system", "subtype": "init", "session_id": id, "model": model})
				_ = os.WriteFile(state, []byte("failed"), 0o644)
				fail("error: failed to run prompt: provider.connection_error: Request timed out.")
			}
			_ = os.WriteFile(state, []byte("resumed:"+convID), 0o644)
		}
	case strings.Contains(prompt, "FAIL_429"):
		fail("Error: rate limit exceeded — usage quota is spent, resets soon")
	case strings.Contains(prompt, "FAIL_403"):
		fail("Error: 403 forbidden: authentication required")
	case strings.Contains(prompt, "FAIL_SESSION_NOT_FOUND"):
		fail("Session not found: " + id)
	case strings.Contains(prompt, "FAIL_DESKTOP"):
		fail("This session is in use by another Kimi Code Desktop instance.")
	}

	emit(map[string]any{"type": "system", "subtype": "init", "session_id": id, "model": model})

	result := "echo: " + prompt
	if strings.Contains(prompt, "tool") {
		call := map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"go test ./..."}`}}
		emit(map[string]any{"role": "assistant", "tool_calls": []any{call}})
		emit(map[string]any{"role": "tool", "name": "Bash", "tool_call_id": "call-1", "content": "ok"})
		result = "ran the tool"
	}
	if strings.Contains(prompt, "ask-user-marker") {
		result = "Before I proceed:\n```ask-user\n{\"question\":\"Which timeout should I keep?\",\"options\":[\"Keep 2ms\",\"Keep 5ms\"]}\n```"
	}
	if strings.Contains(prompt, "proceeding") || strings.Contains(prompt, "Keep 2ms") {
		result = "proceeding with the chosen budget"
	}
	if strings.Contains(prompt, "resume-demo turn 2") {
		result = "step 2: continuing after step 1"
	}

	emit(map[string]any{"role": "assistant", "content": result})
	emit(map[string]any{
		"type": "result", "stop_reason": "success", "result": result,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
	})
}

func fakeACP() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	sessionID := "kimi-fake-session-1"
	for {
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		respond := func(result any) {
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		}
		fail := func(code int, message string) {
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": code, "message": message},
			})
		}
		update := func(v any) {
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "method": "session/update",
				"params": map[string]any{"sessionId": sessionID, "update": v},
			})
		}

		switch req.Method {
		case "initialize":
			respond(map[string]any{"agentInfo": map[string]any{"name": "fakekimi", "version": "1"}})
		case "session/new":
			if state := os.Getenv("FAKEKIMI_STATE_FILE"); state != "" {
				if _, err := os.Stat(state); os.IsNotExist(err) {
					sessionID = "kimi-fake-transient-id"
				}
			}
			respond(map[string]any{"sessionId": sessionID})
		case "session/resume":
			var p struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.SessionID != "" {
				sessionID = p.SessionID
			}
			respond(map[string]any{})
		case "session/set_mode", "session/set_model", "session/set_config_option":
			respond(nil)
		case "session/prompt":
			var p struct {
				Prompt []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			}
			_ = json.Unmarshal(req.Params, &p)
			prompt := ""
			if len(p.Prompt) > 0 {
				prompt = p.Prompt[0].Text
			}
			if strings.Contains(prompt, "FAIL_TRANSIENT_ONCE") {
				state := os.Getenv("FAKEKIMI_STATE_FILE")
				if state != "" {
					if _, err := os.Stat(state); os.IsNotExist(err) {
						sessionID = "kimi-fake-transient-id"
						_ = os.WriteFile(state, []byte("failed"), 0o644)
						fail(-32000, "error: failed to run prompt: provider.connection_error: Request timed out.")
						continue
					}
					_ = os.WriteFile(state, []byte("resumed:"+sessionID), 0o644)
				}
			}
			switch {
			case strings.Contains(prompt, "FAIL_429"):
				fail(429, "Error: rate limit exceeded — usage quota is spent, resets soon")
				continue
			case strings.Contains(prompt, "FAIL_403"):
				fail(-32000, "Error: 403 forbidden: authentication required")
				continue
			case strings.Contains(prompt, "FAIL_SESSION_NOT_FOUND"):
				fail(-32602, "Session not found: "+sessionID)
				continue
			case strings.Contains(prompt, "FAIL_DESKTOP"):
				fail(-32602, "This session is in use by another Kimi Code Desktop instance.")
				continue
			}

			result := "echo: " + prompt
			if strings.Contains(prompt, "tool") {
				result = "ran the tool"
			}
			if strings.Contains(prompt, "ask-user-marker") {
				result = "Before I proceed:\n```ask-user\n{\"question\":\"Which timeout should I keep?\",\"options\":[\"Keep 2ms\",\"Keep 5ms\"]}\n```"
			}
			if strings.Contains(prompt, "proceeding") || strings.Contains(prompt, "Keep 2ms") {
				result = "proceeding with the chosen budget"
			}
			if strings.Contains(prompt, "resume-demo turn 2") {
				result = "step 2: continuing after step 1"
			}
			update(map[string]any{
				"sessionUpdate": "agent_thought_chunk",
				"content":       map[string]any{"type": "text", "text": "thinking about the request"},
			})
			if strings.Contains(prompt, "tool") {
				update(map[string]any{
					"sessionUpdate": "tool_call", "toolCallId": "call-1", "kind": "execute",
					"title": "go test ./...", "rawInput": map[string]any{"command": "go test ./..."},
				})
				update(map[string]any{
					"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed",
					"rawOutput": map[string]any{"output": "ok"},
				})
			}
			update(map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": result},
			})
			update(map[string]any{
				"sessionUpdate": "usage_update",
				"usage":         map[string]any{"input_tokens": 10, "output_tokens": 5},
			})
			respond(map[string]any{"stopReason": "end_turn"})
		case "session/close":
			respond(nil)
			return
		default:
			fail(-32601, "method not found: "+req.Method)
		}
	}
}
