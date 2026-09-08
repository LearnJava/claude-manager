package experience

import (
	"strings"
	"testing"
	"time"

	"claude-manager/internal/store"
)

// TestStepDurSec_ParallelAndHeartbeat computes durations from two of the
// fixtures already used by mdlog_test.go — parallel.md (three tool_use steps
// sharing one timestamp, three results sharing a later one) and
// heartbeat-error.md (one long call with tool_progress heartbeats in
// between, closed by an "error" entry) — and checks the values against a
// manual read of the fixture's own timestamps (LEARN-TASKS.md LN-18 "тест
// расчёта длительности на фикстуре с параллельными вызовами и хартбитами").
func TestStepDurSec_ParallelAndHeartbeat(t *testing.T) {
	traj, err := ParseLogFile(fixtureParallel)
	if err != nil {
		t.Fatalf("ParseLogFile(parallel): %v", err)
	}
	var toolSteps []Step
	for _, s := range traj.Steps {
		if s.Kind == StepToolUse {
			toolSteps = append(toolSteps, s)
		}
	}
	if len(toolSteps) != 3 {
		t.Fatalf("got %d tool_use steps, want 3", len(toolSteps))
	}
	// All three calls are logged at 11:39:02 and all three results at
	// 11:39:05 — every one of them took exactly 3 seconds, whichever result
	// the FIFO queue bound to which call.
	for i, s := range toolSteps {
		if got := stepDurSec(s); got != 3 {
			t.Errorf("parallel step %d (%s) DurSec = %d, want 3", i, s.ToolName, got)
		}
	}

	traj, err = ParseLogFile(fixtureHeartbeat)
	if err != nil {
		t.Fatalf("ParseLogFile(heartbeat): %v", err)
	}
	var bash *Step
	for i := range traj.Steps {
		if traj.Steps[i].Kind == StepToolUse {
			bash = &traj.Steps[i]
			break
		}
	}
	if bash == nil {
		t.Fatal("no tool_use step found in heartbeat-error.md")
	}
	// 12:00:01 (tool) -> 12:01:45 (error) = 104s. The two tool_progress
	// heartbeats in between (12:00:30, 12:01:00) must not have closed the
	// call early or reset its start time.
	if got := stepDurSec(*bash); got != 104 {
		t.Errorf("heartbeat step DurSec = %d, want 104", got)
	}
	if !bash.ResultIsError {
		t.Error("heartbeat step ResultIsError = false, want true (closed by an \"error\" entry)")
	}
}

// TestStepDurSec_NoResult: a call whose result never arrived (the read
// window ended mid-call) must report DurSec == 0, not a bogus negative or
// wildly large value from a zero time.Time — and actionRows must carry that
// 0 through unchanged, so DurationProfile's callers can rely on "0 means
// unknown" (LEARN-TASKS.md LN-18: "тест что вызов без результата даёт
// dur_sec = 0 и не искажает медиану").
func TestStepDurSec_NoResult(t *testing.T) {
	now := time.Now().UTC()
	pending := Step{Index: 0, Kind: StepToolUse, ToolName: "Bash", InputText: "sleep 300", Time: now}
	if got := stepDurSec(pending); got != 0 {
		t.Errorf("DurSec of a call with no result = %d, want 0", got)
	}

	traj := Trajectory{ProjectPath: "/proj", Steps: []Step{pending}}
	rows := actionRows(traj, "proj", "S1", nil, "cli-1", "")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].DurSec != 0 {
		t.Errorf("ActionRow.DurSec = %d, want 0", rows[0].DurSec)
	}
}

// TestDurationProfile_MinSamplesAndProjectIsolation seeds two projects with
// different timing characters — a fast Go project whose one recurring
// command never reaches the reporting threshold, and a slow Rust project
// whose recurring command does — and checks DurationProfile keeps them
// separate and applies both the n>=10 floor and the median/p90/max/fail-rate
// math correctly (LEARN-TASKS.md LN-18: "тест профиля на двух проектах с
// разным характером (быстрый Go / медленный Rust)").
func TestDurationProfile_MinSamplesAndProjectIsolation(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	var rows []store.ActionRow
	// "fastgo": 12 calls of `go test ./...`, each a few seconds — well under
	// MinDurationSamples's floor isn't the point here (12 >= 10), but the
	// median stays well under the primer's 60s threshold either way. One
	// call fails.
	fastDurs := []int64{2, 2, 3, 3, 3, 4, 4, 4, 5, 5, 6, 6}
	for i, d := range fastDurs {
		rows = append(rows, store.ActionRow{
			Project: "fastgo", Session: "S1", StepIndex: i, Tool: "Bash",
			Sig: "Bash:go test", Arg: "go test ./...", DurSec: d,
			IsError:   i == 0,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}
	// "slowrust": 9 calls of a slow build — one short of the reporting
	// floor, so it must not appear at all.
	for i := 0; i < 9; i++ {
		rows = append(rows, store.ActionRow{
			Project: "slowrust", Session: "S1", StepIndex: i, Tool: "Bash",
			Sig: "Bash:cargo build", Arg: "cargo build --release", DurSec: 300,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}
	// "slowrust": a second signature with exactly 10 samples, durations
	// 100..1000s in steps of 100 (median 550, p90 interpolated, max 1000),
	// two failures.
	rustDurs := []int64{100, 200, 300, 400, 500, 600, 700, 800, 900, 1000}
	for i, d := range rustDurs {
		rows = append(rows, store.ActionRow{
			Project: "slowrust", Session: "S1", StepIndex: 100 + i, Tool: "Bash",
			Sig: "Bash:cargo test", Arg: "cargo test --release", DurSec: d,
			IsError:   i < 2,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}
	// A zero-duration row (no result ever arrived) must never be counted —
	// store.ActionDurations filters it out at the SQL level.
	rows = append(rows, store.ActionRow{
		Project: "slowrust", Session: "S1", StepIndex: 999, Tool: "Bash",
		Sig: "Bash:cargo test", Arg: "cargo test --release", DurSec: 0,
		Timestamp: now,
	})
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	fastProfile, err := DurationProfile(s, "fastgo")
	if err != nil {
		t.Fatalf("DurationProfile(fastgo): %v", err)
	}
	if len(fastProfile) != 1 {
		t.Fatalf("fastgo profile has %d signatures, want 1: %+v", len(fastProfile), fastProfile)
	}
	if fastProfile[0].Count != 12 {
		t.Errorf("fastgo Count = %d, want 12", fastProfile[0].Count)
	}
	if fastProfile[0].MedianSec != 4 {
		t.Errorf("fastgo MedianSec = %v, want 4", fastProfile[0].MedianSec)
	}

	rustProfile, err := DurationProfile(s, "slowrust")
	if err != nil {
		t.Fatalf("DurationProfile(slowrust): %v", err)
	}
	// cargo build (9 samples) must be dropped; only cargo test (10 samples)
	// survives the MinDurationSamples floor.
	if len(rustProfile) != 1 {
		t.Fatalf("slowrust profile has %d signatures, want 1 (cargo build under the n>=10 floor): %+v", len(rustProfile), rustProfile)
	}
	got := rustProfile[0]
	if got.Sig != "Bash:cargo test" {
		t.Fatalf("slowrust signature = %q, want Bash:cargo test", got.Sig)
	}
	if got.Count != 10 {
		t.Errorf("Count = %d, want 10 (the zero-duration row must not count)", got.Count)
	}
	if got.MedianSec != 550 {
		t.Errorf("MedianSec = %v, want 550", got.MedianSec)
	}
	if got.MaxSec != 1000 {
		t.Errorf("MaxSec = %v, want 1000", got.MaxSec)
	}
	if got.TotalSec != 5500 {
		t.Errorf("TotalSec = %v, want 5500", got.TotalSec)
	}
	if got.FailRate != 0.2 {
		t.Errorf("FailRate = %v, want 0.2 (2 of 10)", got.FailRate)
	}
}

// TestDurationSection covers the primer's timing block (LEARN-TASKS.md
// LN-18): the 60s median threshold, the 5-line cap, and the dedicated
// `sleep` line with a summed total instead of a per-command timeout tip.
func TestDurationSection(t *testing.T) {
	t.Run("empty when nothing clears the threshold", func(t *testing.T) {
		profile := []SignatureDuration{{Sig: "Bash:git status", MedianSec: 1, Count: 50}}
		if got := durationSection(profile); got != "" {
			t.Errorf("durationSection() = %q, want \"\"", got)
		}
	})

	t.Run("qualifying commands get a timeout tip", func(t *testing.T) {
		profile := []SignatureDuration{
			{Sig: "Bash:go test ./...", MedianSec: 125, Count: 20},
		}
		got := durationSection(profile)
		want := "Command timing (this project):\n" +
			"`go test ./...` takes ~2min in this project — set a generous timeout or split the work"
		if got != want {
			t.Errorf("durationSection() =\n%q\nwant\n%q", got, want)
		}
	})

	t.Run("sleep gets its own line with total time, not a timeout tip", func(t *testing.T) {
		profile := []SignatureDuration{
			{Sig: "Bash:sleep <ARG>", MedianSec: 120, Count: 5, TotalSec: 900},
		}
		got := durationSection(profile)
		if want := "5 times"; !strings.Contains(got, want) {
			t.Errorf("durationSection() = %q, want it to mention %q", got, want)
		}
		if strings.Contains(got, "set a generous timeout") {
			t.Errorf("durationSection() = %q, sleep must not get the generic timeout tip", got)
		}
	})

	t.Run("capped at 5 lines", func(t *testing.T) {
		var profile []SignatureDuration
		for i := 0; i < 8; i++ {
			profile = append(profile, SignatureDuration{
				Sig: "Bash:cmd" + string(rune('a'+i)), MedianSec: 300 - float64(i), Count: 20,
			})
		}
		got := durationSection(profile)
		lines := 0
		for _, c := range got {
			if c == '\n' {
				lines++
			}
		}
		// header line + 5 command lines = 6 newline-separated lines, i.e. 5 "\n".
		if lines != 5 {
			t.Errorf("durationSection() has %d newlines, want 5 (header + 5 lines, capped)", lines)
		}
	})

	t.Run("below-threshold entries never count against the cap", func(t *testing.T) {
		profile := []SignatureDuration{
			{Sig: "Bash:go test", MedianSec: 90, Count: 20},
			{Sig: "Bash:git status", MedianSec: 1, Count: 50},
		}
		got := durationSection(profile)
		if strings.Contains(got, "git status") {
			t.Errorf("durationSection() = %q, must not mention a sub-threshold signature", got)
		}
	})
}
