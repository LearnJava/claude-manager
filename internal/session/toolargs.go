package session

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

const (
	toolArgValueMax  = 300  // per-value cap, in runes
	toolArgsTotalMax = 2048 // whole-map cap, in bytes of key+value
)

// BuildToolArgs flattens a tool_use input object into scalar string pairs for
// LogEntry.ToolArgs. Strings, numbers and bools are kept (values truncated);
// arrays and objects become a count ("5 items", "3 fields"); null is skipped.
// Keys are taken in sorted order until the total size budget is spent, so the
// result is deterministic. Returns nil for empty or non-object input.
func BuildToolArgs(input json.RawMessage) map[string]string {
	if len(input) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil || len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(m))
	total := 0
	for _, k := range keys {
		var v string
		switch x := m[k].(type) {
		case string:
			v = x
		case float64:
			v = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			v = strconv.FormatBool(x)
		case []any:
			v = fmt.Sprintf("%d items", len(x))
		case map[string]any:
			v = fmt.Sprintf("%d fields", len(x))
		default:
			continue
		}
		if r := []rune(v); len(r) > toolArgValueMax {
			v = string(r[:toolArgValueMax]) + "…"
		}
		if total+len(k)+len(v) > toolArgsTotalMax {
			break
		}
		total += len(k) + len(v)
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
