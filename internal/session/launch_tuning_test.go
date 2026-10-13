package session

import (
	"testing"

	"claude-manager/internal/config"
	"claude-manager/internal/store"
)

func runsWithTurns(status string, turns ...int) []*store.SessionRun {
	out := make([]*store.SessionRun, 0, len(turns))
	for _, n := range turns {
		out = append(out, &store.SessionRun{Status: status, NumTurns: n})
	}
	return out
}

func TestMaxTurnsFromHistory(t *testing.T) {
	cases := []struct {
		name  string
		runs  []*store.SessionRun
		floor int
		want  int
	}{
		{"no history → no cap", nil, 50, 0},
		{"too few samples → no cap", runsWithTurns("completed", 10, 20, 30, 40), 50, 0},
		{"floor wins over small history", runsWithTurns("completed", 5, 8, 10, 12, 18), 50, 50},
		{"worst × 1.6, rounded up", runsWithTurns("completed", 40, 60, 70, 80, 90), 50, 144},
		{"unfinished/error runs still count", append(runsWithTurns("unfinished", 100), runsWithTurns("error", 1, 2, 3, 4)...), 10, 160},
		{"running and zero-turn runs ignored",
			append(runsWithTurns("running", 999), append(runsWithTurns("completed", 0, 0), runsWithTurns("completed", 10, 10, 10, 10)...)...), 0, 0},
	}
	for _, c := range cases {
		if got := maxTurnsFromHistory(c.runs, c.floor); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestBuildCLIArgs_LaunchTuning(t *testing.T) {
	off := New(Params{Config: config.SessionConfig{Model: "sonnet"}})
	args := off.buildCLIArgs(false)
	if hasFlag(args, "--max-turns") || hasFlag(args, "--exclude-dynamic-system-prompt-sections") {
		t.Errorf("tuning flags present with tuning off: %v", args)
	}

	on := New(Params{Config: config.SessionConfig{Model: "sonnet"}, MaxTurns: 144, ExcludeDynamic: true})
	args = on.buildCLIArgs(false)
	if !hasFlag(args, "--exclude-dynamic-system-prompt-sections") {
		t.Errorf("missing --exclude-dynamic-system-prompt-sections: %v", args)
	}
	found := false
	for i, a := range args {
		if a == "--max-turns" && i+1 < len(args) && args[i+1] == "144" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing --max-turns 144: %v", args)
	}
}
