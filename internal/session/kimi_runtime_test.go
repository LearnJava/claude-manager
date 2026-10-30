package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

func buildFakekimi(t *testing.T) string {
	t.Helper()
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not in PATH: " + err.Error())
	}
	name := "fakekimi"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(t.TempDir(), name)
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(goExe, "build", "-o", out, "./cmd/fakekimi/")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakekimi: %v\n%s", err, b)
	}
	return out
}

func TestKimiParser_InitAssistantToolResult(t *testing.T) {
	p := newKimiStream()
	inits := p.Parse(`{"type":"system","subtype":"init","session_id":"s-1","model":"kimi-code/kimi-for-coding"}`)
	if len(inits) != 1 || inits[0].EventType != EventInit || inits[0].Init.SessionID != "s-1" {
		t.Fatalf("init: %+v", inits)
	}

	evs := p.Parse(`{"role":"assistant","content":"working on it","tool_calls":[{"id":"c1","name":"Bash","arguments":"{\"command\":\"ls\"}"}]}`)
	if len(evs) != 1 || len(evs[0].Entries) != 2 {
		t.Fatalf("assistant: %+v", evs)
	}
	if evs[0].Entries[0].Message != "working on it" || evs[0].Entries[0].Level != "text" {
		t.Errorf("text entry: %+v", evs[0].Entries[0])
	}
	tool := evs[0].Entries[1]
	if tool.Level != "tool" || tool.ToolName != "Bash" || tool.ToolUseID != "c1" {
		t.Errorf("tool entry: %+v", tool)
	}

	res := p.Parse(`{"role":"tool","name":"Bash","tool_call_id":"c1","content":"file.go"}`)
	if len(res) != 1 || res[0].Entries[0].Level != "tool_result" || res[0].Entries[0].ToolUseID != "c1" {
		t.Fatalf("tool_result: %+v", res)
	}

	done := p.Parse(`{"type":"result","stop_reason":"success","result":"done","usage":{"input_tokens":10,"output_tokens":5}}`)
	var result *SessionResult
	var sawUsage bool
	for _, ev := range done {
		if ev.EventType == EventResult {
			result = ev.Result
		}
		if ev.Usage != nil {
			sawUsage = true
		}
	}
	if result == nil || result.StopReason != "success" || result.ResultText != "done" {
		t.Fatalf("result: %+v", result)
	}
	if !sawUsage {
		t.Error("expected a usage event from the result's tokens")
	}
}

func TestKimiParser_SyntheticToolIDAndError(t *testing.T) {
	p := newKimiStream()
	p.Parse(`{"role":"assistant","tool_calls":[{"name":"Read","arguments":"{\"path\":\"a.go\"}"}]}`)
	res := p.Parse(`{"role":"tool","name":"Read","content":"x"}`)
	if got := res[0].Entries[0].ToolUseID; got != "kimi-1" {
		t.Errorf("synthetic id = %q, want kimi-1", got)
	}

	done := p.Parse(`{"type":"result","stop_reason":"error","error":"boom","exit_code":1}`)
	var result *SessionResult
	for _, ev := range done {
		if ev.EventType == EventResult {
			result = ev.Result
		}
	}
	if result == nil || result.StopReason != "error" {
		t.Fatalf("error result: %+v", result)
	}
	if p.LastError() != "boom" {
		t.Errorf("LastError = %q", p.LastError())
	}
}

func TestKimiParser_MalformedLinePreserved(t *testing.T) {
	p := newKimiStream()
	evs := p.Parse("not json at all")
	if len(evs) != 1 || evs[0].Entries[0].Message != "not json at all" {
		t.Fatalf("fallback: %+v", evs)
	}
}

func TestKimiParser_MetaHintAndFlushResult(t *testing.T) {
	p := newKimiStream()
	p.Parse(`{"role":"meta","type":"system.version","version":"2.4.0"}`)
	inits := p.Parse(`{"role":"meta","type":"session.resume_hint","session_id":"session_abc"}`)
	if len(inits) != 1 || inits[0].EventType != EventInit || inits[0].Init.SessionID != "session_abc" {
		t.Fatalf("meta hint: %+v", inits)
	}

	p.Parse(`{"role":"assistant","tool_calls":[{"type":"function","id":"tool_1","function":{"name":"Read","arguments":"{\"path\":\"a.txt\"}"}}]}`)
	res := p.Parse(`{"role":"tool","tool_call_id":"tool_1","content":"data"}`)
	if res[0].Entries[0].ToolUseID != "tool_1" || res[0].Entries[0].Level != "tool_result" {
		t.Fatalf("tool result: %+v", res)
	}

	// No result event on the real wire: Flush must synthesize one.
	out := p.Flush()
	if len(out) != 1 || out[0].EventType != EventResult || out[0].Result.StopReason != "success" {
		t.Fatalf("flush: %+v", out)
	}
	if again := p.Flush(); again != nil {
		t.Fatalf("second flush must be empty: %+v", again)
	}
}

func TestKimiParser_MetaMessagesAreReadable(t *testing.T) {
	p := newKimiStream()
	version := p.Parse(`{"role":"meta","type":"system.version","version":"2.4.0"}`)
	if got := version[0].Entries[0].Message; got != "Kimi Code 2.4.0" {
		t.Fatalf("version message = %q", got)
	}

	retry := p.Parse(`{"role":"meta","type":"turn.step.retrying","failed_attempt":7,"next_attempt":8,"max_attempts":10,"delay_ms":35819.225,"error_name":"APITimeoutError","error_message":"Request timed out."}`)
	got := retry[0].Entries[0].Message
	if !strings.Contains(got, "Request timed out") || !strings.Contains(got, "retry 8/10") || !strings.Contains(got, "36s") {
		t.Fatalf("retry message = %q", got)
	}
	if strings.Contains(got, `"role"`) {
		t.Fatalf("retry still shows raw JSON: %q", got)
	}
}

func TestKimiRuntime_TransientTimeoutRetriesAndResumes(t *testing.T) {
	bin := buildFakekimi(t)
	state := filepath.Join(t.TempDir(), "kimi-retry-state")
	t.Setenv("FAKEKIMI_STATE_FILE", state)

	texts := make(chan string, 4)
	s := New(Params{
		ID: "kimi/retry", ProjectName: "kimi", ProjectPath: t.TempDir(),
		KimiPath: bin, RetryDelay: 1,
		Config: config.SessionConfig{
			Name: "retry", Runtime: config.RuntimeKimi,
			Prompt: "FAIL_TRANSIENT_ONCE", AutoRestart: false,
		},
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtLog && ev.Entry != nil && ev.Entry.Level == "text" {
				texts <- ev.Entry.Message
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.Run(ctx)

	select {
	case text := <-texts:
		if !strings.Contains(text, "echo: FAIL_TRANSIENT_ONCE") {
			t.Fatalf("recovered text = %q", text)
		}
	default:
		t.Fatal("no successful turn after the transient timeout")
	}
	data, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read retry state: %v", err)
	}
	if got := string(data); got != "resumed:kimi-fake-transient-id" {
		t.Fatalf("retry state = %q, want the same Kimi session id", got)
	}
	if status := s.Status(); status != config.StatusIdle {
		t.Fatalf("status after recovery = %q, want idle", status)
	}
}
