package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleUser_TaskNotificationIsSystemEntry(t *testing.T) {
	note := "<task-notification>\n<task-id>bh4wmyqj8</task-id>\n<tool-use-id>toolu_01J9</tool-use-id>\n" +
		"<output-file>C:\\Temp\\tasks\\bh4wmyqj8.output</output-file>\n<status>completed</status>\n" +
		"<summary>Background command \"cargo test &gt; .tmp_j.log 2&gt;&amp;1\" completed (exit code 0)</summary>\n" +
		"</task-notification>"
	content, _ := json.Marshal(note)
	str := `{"type":"user","message":{"role":"user","content":` + string(content) + `}}`
	arr := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":` + string(content) + `}]}}`

	want := "Background task completed: Background command \"cargo test > .tmp_j.log 2>&1\" completed (exit code 0)\n" +
		"task-id: bh4wmyqj8\ntool-use-id: toolu_01J9\noutput-file: C:\\Temp\\tasks\\bh4wmyqj8.output"
	for name, line := range map[string]string{"string": str, "blocks": arr} {
		ev := ParseLine(line)
		if len(ev.Entries) != 1 {
			t.Fatalf("%s: entries = %d, want 1", name, len(ev.Entries))
		}
		e := ev.Entries[0]
		if e.Level != "system" {
			t.Errorf("%s: level = %q, want system", name, e.Level)
		}
		if e.Message != want {
			t.Errorf("%s: message =\n%s\nwant\n%s", name, e.Message, want)
		}
	}
}

func TestFormatTaskNotifications_MultilineFieldAndSurroundingText(t *testing.T) {
	got := formatTaskNotifications("<task-notification><status>failed</status><result>line1\nline2</result></task-notification>\nplease check")
	want := "Background task failed\nresult:\nline1\nline2\n\nplease check"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	if !strings.HasPrefix(got, "Background task") {
		t.Fatal("prefix")
	}
}

func TestHandleUser_PlainPromptStaysUser(t *testing.T) {
	ev := ParseLine(`{"type":"user","message":{"role":"user","content":"fix the bug"}}`)
	if len(ev.Entries) != 1 || ev.Entries[0].Level != "user" {
		t.Fatalf("got %+v", ev.Entries)
	}
}
