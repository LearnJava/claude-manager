package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildToolArgs(t *testing.T) {
	got := BuildToolArgs(json.RawMessage(`{"command":"ls","description":"list"}`))
	if len(got) != 2 || got["command"] != "ls" || got["description"] != "list" {
		t.Errorf("bash: %v", got)
	}
	got = BuildToolArgs(json.RawMessage(`{"file_path":"a.go","offset":10,"limit":50}`))
	if len(got) != 3 || got["offset"] != "10" || got["limit"] != "50" {
		t.Errorf("read: %v", got)
	}
	got = BuildToolArgs(json.RawMessage(`{"todos":[1,2,3],"ok":true,"n":null}`))
	if got["todos"] != "3 items" || got["ok"] != "true" || len(got) != 2 {
		t.Errorf("nested: %v", got)
	}
	if BuildToolArgs(nil) != nil || BuildToolArgs(json.RawMessage(`[1]`)) != nil {
		t.Error("expected nil for empty/non-object")
	}
}

func TestBuildToolArgs_Limits(t *testing.T) {
	big := strings.Repeat("я", 100000)
	got := BuildToolArgs(json.RawMessage(`{"file_path":"x","content":"` + big + `"}`))
	if n := len([]rune(got["content"])); n > toolArgValueMax+1 {
		t.Errorf("content not truncated: %d runes", n)
	}
	var parts []string
	for i := 0; i < 50; i++ {
		parts = append(parts, `"k`+strings.Repeat("a", i)+`":"`+strings.Repeat("v", 200)+`"`)
	}
	got = BuildToolArgs(json.RawMessage("{" + strings.Join(parts, ",") + "}"))
	total := 0
	for k, v := range got {
		total += len(k) + len(v)
	}
	if total > toolArgsTotalMax {
		t.Errorf("map too big: %d", total)
	}
}

func TestParseToolUse_ToolArgs(t *testing.T) {
	got := BuildToolArgs(json.RawMessage(`{"command":"dir","timeout":5}`)) // hermes terminal shape
	if got["command"] != "dir" {
		t.Errorf("%v", got)
	}
	got = BuildToolArgs(json.RawMessage(`{"path":"a.txt","offset":1}`)) // hermes read_file
	if got["path"] != "a.txt" || got["offset"] != "1" {
		t.Errorf("%v", got)
	}
}
