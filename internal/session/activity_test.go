package session

import (
	"reflect"
	"testing"
)

func activityKinds(evs []ParsedEvent) []string {
	var out []string
	for _, ev := range evs {
		if ev.Activity != nil {
			k := ev.Activity.Kind
			if ev.Activity.Tool != "" {
				k += ":" + ev.Activity.Tool
			}
			out = append(out, k)
		}
	}
	return out
}

func TestParseStreamEventActivity(t *testing.T) {
	lines := []string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c1","name":"Read","input":{}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}}`,
		`{"type":"result","subtype":"success","usage":{"input_tokens":1,"output_tokens":1}}`,
	}
	var evs []ParsedEvent
	for _, l := range lines {
		evs = append(evs, parseLineAt(l, testTime))
	}
	want := []string{"thinking", "tool:Read", "writing", "idle"}
	if got := activityKinds(evs); !reflect.DeepEqual(got, want) {
		t.Fatalf("activity = %v, want %v", got, want)
	}
	for _, ev := range evs[:6] {
		if len(ev.Entries) != 0 {
			t.Errorf("stream_event must not produce log entries, got %v", ev.Entries)
		}
	}
}

func TestHermesStream_Activity(t *testing.T) {
	h := newHermesStream()
	var evs []ParsedEvent
	for _, l := range []string{
		`{"type":"text","text":"hel"}`,
		`{"type":"text","text":"lo"}`,
		`{"type":"tool_use","name":"terminal","tool_call_id":"t1","input":{"command":"ls"}}`,
		`{"type":"tool_result","name":"terminal","output":"x","duration_ms":5}`,
		`{"type":"text","text":"done"}`,
		`{"type":"result","exit_code":0,"text":"done"}`,
	} {
		evs = append(evs, h.Parse(l)...)
	}
	want := []string{"writing", "tool:terminal", "thinking", "writing", "idle"}
	if got := activityKinds(evs); !reflect.DeepEqual(got, want) {
		t.Fatalf("activity = %v, want %v", got, want)
	}
}

func TestSessionSetActivityDedup(t *testing.T) {
	var got []SessionEvent
	s := &Session{onEvent: func(_ string, ev SessionEvent) { got = append(got, ev) }}
	s.setActivity(Activity{Kind: ActivityThinking})
	s.setActivity(Activity{Kind: ActivityThinking})
	s.setActivity(Activity{Kind: ActivityTool, Tool: "Read"})
	s.setActivity(Activity{Kind: ActivityTool, Tool: "Bash"})
	if len(got) != 3 || got[0].Type != EvtActivity || got[2].Activity.Tool != "Bash" || got[0].Activity.Since.IsZero() {
		t.Fatalf("unexpected events: %+v", got)
	}
}
