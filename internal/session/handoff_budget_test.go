package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"claude-manager/internal/config"
)

// assistantUsageLine is one stream line of a model call that sent ctx tokens
// of context (split like a real cached call: most of it read from cache).
func assistantUsageLine(ctx int) string {
	data, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"model":   "claude-sonnet-5",
			"content": []map[string]any{{"type": "text", "text": "working"}},
			"usage": map[string]any{
				"input_tokens":                3,
				"cache_creation_input_tokens": 1000,
				"cache_read_input_tokens":     ctx - 1003,
				"output_tokens":               50,
			},
		},
	})
	return string(data)
}

func newHandoffSession(budget int, logs *[]config.LogEntry) *Session {
	s := New(Params{
		ID:     "p/s",
		Config: config.SessionConfig{Name: "s", AutoRestart: true, HandoffContextTokens: budget},
		OnEvent: func(_ string, ev SessionEvent) {
			if logs != nil && ev.Type == EvtLog && ev.Entry != nil && ev.Entry.Source == "manager" {
				*logs = append(*logs, *ev.Entry)
			}
		},
	})
	s.inputCh = make(chan []byte, 16)
	return s
}

// queuedNotes drains the session's stdin channel and returns the user
// messages written to it.
func queuedNotes(t *testing.T, s *Session) []string {
	t.Helper()
	var out []string
	for {
		select {
		case line := <-s.inputCh:
			var msg struct {
				Message struct {
					Content any `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal(line, &msg); err != nil {
				t.Fatalf("queued input is not a user message: %v: %s", err, line)
			}
			out = append(out, fmt.Sprint(msg.Message.Content))
		default:
			return out
		}
	}
}

func appendedPrompt(args []string) string {
	if i := slices.Index(args, "--append-system-prompt"); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func TestHandoffBudget_OffKeepsPromptByteIdentical(t *testing.T) {
	s := newHandoffSession(0, nil)
	if got, want := appendedPrompt(s.buildCLIArgs(true)), askUserProtocolPrompt+"\n\n"+backgroundTaskWarningPrompt; got != want {
		t.Errorf("with handoff_context_tokens = 0 the autonomous prompt must not change:\n%s", got)
	}
	s.handleLine(assistantUsageLine(500_000), true)
	if notes := queuedNotes(t, s); len(notes) != 0 {
		t.Errorf("off: nothing may be written to stdin, got %q", notes)
	}
}

func TestHandoffBudget_PromptCarriesThisRunsTag(t *testing.T) {
	s := newHandoffSession(150_000, nil)
	prompt := appendedPrompt(s.buildCLIArgs(true))
	if len(s.handoffTag) != 8 {
		t.Fatalf("buildCLIArgs must mint an 8-char tag, got %q", s.handoffTag)
	}
	if !strings.Contains(prompt, "[session-manager "+s.handoffTag+"]") {
		t.Errorf("the system prompt must announce the tagged note, got:\n%s", prompt)
	}
	if strings.Contains(appendedPrompt(newHandoffSession(150_000, nil).buildCLIArgs(false)), "session-manager") {
		t.Error("an interactive run has no slice to hand off — no handoff prompt")
	}
}

func TestHandoffBudget_NoteOnceWhenACallCrossesTheBudget(t *testing.T) {
	var logs []config.LogEntry
	s := newHandoffSession(150_000, &logs)
	s.buildCLIArgs(true)

	s.handleLine(assistantUsageLine(149_999), true)
	if notes := queuedNotes(t, s); len(notes) != 0 {
		t.Fatalf("below the budget nothing is sent, got %q", notes)
	}
	s.handleLine(assistantUsageLine(152_000), true)
	s.handleLine(assistantUsageLine(152_000), true) // the same call's next content block
	notes := queuedNotes(t, s)
	if len(notes) != 1 {
		t.Fatalf("one note for the call that crossed the budget, got %d: %q", len(notes), notes)
	}
	for _, want := range []string{"[session-manager " + s.handoffTag + "]", "152k", "150k", "continue_session", "commit"} {
		if !strings.Contains(notes[0], want) {
			t.Errorf("note lacks %q: %s", want, notes[0])
		}
	}
	if len(logs) != 1 || !strings.Contains(logs[0].Message, "note 1 of 3") {
		t.Errorf("the feed must say why the agent was asked to hand off, got %+v", logs)
	}
}

func TestHandoffBudget_RepeatsEveryNCallsUpToTheCap(t *testing.T) {
	s := newHandoffSession(150_000, nil)
	s.buildCLIArgs(true)
	sent := 0
	for call := 0; call < 200; call++ {
		s.handleLine(assistantUsageLine(150_000+call), true)
		sent += len(queuedNotes(t, s))
		switch call {
		case 0:
			if sent != 1 {
				t.Fatalf("first over-budget call: 1 note, got %d", sent)
			}
		case handoffRepeatEvery - 1:
			if sent != 1 {
				t.Fatalf("no reminder before %d more calls, got %d notes", handoffRepeatEvery, sent)
			}
		case handoffRepeatEvery:
			if sent != 2 {
				t.Fatalf("reminder after %d more calls, got %d notes", handoffRepeatEvery, sent)
			}
		}
	}
	if sent != handoffMaxNotes {
		t.Errorf("at most %d notes per run, got %d", handoffMaxNotes, sent)
	}
}

func TestHandoffBudget_InteractiveRunIsLeftAlone(t *testing.T) {
	s := newHandoffSession(150_000, nil)
	s.buildCLIArgs(false)
	s.handleLine(assistantUsageLine(400_000), false)
	if notes := queuedNotes(t, s); len(notes) != 0 {
		t.Errorf("interactive: no note, got %q", notes)
	}
}

func TestUTF8Env_AddsMissingKeepsSet(t *testing.T) {
	got := utf8Env([]string{"PATH=C:\\x", "pythonioencoding=cp1251"})
	if !slices.Contains(got, "PYTHONUTF8=1") {
		t.Errorf("PYTHONUTF8 must be added: %q", got)
	}
	if slices.Contains(got, "PYTHONIOENCODING=utf-8") || !slices.Contains(got, "pythonioencoding=cp1251") {
		t.Errorf("a value the user set (any case) must be kept: %q", got)
	}
	if len(got) != 3 {
		t.Errorf("exactly one variable added, got %q", got)
	}
}
