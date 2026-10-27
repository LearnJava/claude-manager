package nightly

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/suggest"
	"claude-manager/internal/worker"
)

func red(cmd, output string) worker.GateResult {
	return worker.GateResult{Commands: []worker.GateCommandResult{
		{Command: "go build ./...", Output: "ok\n"},
		{Command: cmd, Output: output, ExitCode: 1},
	}}
}

func green() worker.GateResult {
	return worker.GateResult{Passed: true, Commands: []worker.GateCommandResult{{Command: "go build ./..."}}}
}

type rig struct {
	t     *testing.T
	root  string
	res   worker.GateResult
	runs  int
	marks []bool
	notes []string
	last  *Last
	job   Job
	deps  Deps
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t, root: t.TempDir(), res: green()}
	sched, err := config.ParseGateSchedule("03:00")
	if err != nil {
		t.Fatal(err)
	}
	r.job = Job{Project: "p", Root: r.root, Schedule: sched, Gates: []string{"go test ./..."},
		QueueFile: "STATUS-P1.md", Queues: []string{"STATUS-P1.md"}}
	r.deps = Deps{
		Last: func(string) (*Last, error) { return r.last, nil },
		Mark: func(_ string, at time.Time, passed bool, note string) error {
			r.marks = append(r.marks, passed)
			r.notes = append(r.notes, note)
			r.last = &Last{At: at}
			return nil
		},
		Run: func(context.Context, string, []string) worker.GateResult { r.runs++; return r.res },
		Mu:  &sync.Mutex{},
	}
	return r
}

func (r *rig) read(name string) string {
	b, err := os.ReadFile(filepath.Join(r.root, name))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		r.t.Fatal(err)
	}
	return string(b)
}

// tick runs the scheduler at now with the scheduler itself started on the 1st.
func (r *rig) tick(now time.Time) []Outcome {
	return RunDue(context.Background(), []Job{r.job}, r.deps, at(1, 0, 0), now)
}

func at(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, time.Local) }

func TestParseGateSchedule(t *testing.T) {
	for _, bad := range []string{"", "3", "25:00", "03:60", "night", "03:00 PM"} {
		if _, err := config.ParseGateSchedule(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	g, err := config.ParseGateSchedule(" 03:30 ")
	if err != nil || g.Hour != 3 || g.Minute != 30 {
		t.Fatalf("got %+v, %v", g, err)
	}
	if got := g.LastDue(at(5, 2, 0)); !got.Equal(at(4, 3, 30)) {
		t.Errorf("before the slot: %v", got)
	}
	if got := g.LastDue(at(5, 3, 30)); !got.Equal(at(5, 3, 30)) {
		t.Errorf("at the slot: %v", got)
	}
	if got := g.LastDue(at(5, 12, 0)); !got.Equal(at(5, 3, 30)) {
		t.Errorf("after the slot: %v", got)
	}
}

func TestGreenGatesFileNothing(t *testing.T) {
	r := newRig(t)
	out := r.tick(at(2, 3, 5))
	if len(out) != 1 || !out[0].Passed || out[0].Pointer != "" || out[0].Err != nil {
		t.Fatalf("outcome %+v", out)
	}
	if r.read(suggest.TaskFile) != "" || r.read("STATUS-P1.md") != "" {
		t.Fatal("green gates wrote a file")
	}
	if len(r.marks) != 1 || !r.marks[0] || r.notes[0] != "" {
		t.Fatalf("marks %v notes %v", r.marks, r.notes)
	}
}

func TestRedGatesFileOneTaskWithError(t *testing.T) {
	r := newRig(t)
	r.res = red("go test ./...", "--- FAIL: TestX (0.01s)\n    x_test.go:12: got 1 want 2\nFAIL\n")
	out := r.tick(at(2, 3, 5))
	if len(out) != 1 || out[0].Passed || out[0].Err != nil || out[0].Pointer == "" {
		t.Fatalf("outcome %+v", out)
	}
	tasks := r.read(suggest.TaskFile)
	for _, want := range []string{"## SG-01:", "go test ./...", "x_test.go:12: got 1 want 2", "сессия не запускалась"} {
		if !strings.Contains(tasks, want) {
			t.Errorf("task file lacks %q:\n%s", want, tasks)
		}
	}
	if strings.Contains(tasks, "go build") {
		t.Errorf("task mentions the passing command:\n%s", tasks)
	}
	if got := strings.TrimSpace(r.read("STATUS-P1.md")); got != out[0].Pointer {
		t.Fatalf("queue %q, pointer %q", got, out[0].Pointer)
	}
}

func TestSameFailureOneTask(t *testing.T) {
	r := newRig(t)
	r.res = red("go test ./...", "--- FAIL: TestX (0.01s)\nFAIL\n")
	r.tick(at(2, 3, 5))
	// The next night: same failure, only the timing differs.
	r.res = red("go test ./...", "--- FAIL: TestX (0.07s)\nFAIL\n")
	out := r.tick(at(3, 3, 5))
	if len(out) != 1 || out[0].Pointer != "" || out[0].Err != nil {
		t.Fatalf("outcome %+v", out)
	}
	if n := strings.Count(r.read(suggest.TaskFile), "## SG-"); n != 1 {
		t.Fatalf("%d tasks", n)
	}
	if n := strings.Count(r.read("STATUS-P1.md"), "\n"); n != 1 {
		t.Fatalf("%d queue lines", n)
	}
	if r.notes[1] != "already queued" {
		t.Errorf("note %q", r.notes[1])
	}
}

func TestOtherFailureIsAnotherTask(t *testing.T) {
	r := newRig(t)
	r.res = red("go test ./...", "FAIL TestX\n")
	r.tick(at(2, 3, 5))
	r.res = red("go test ./...", "FAIL TestY\n")
	r.tick(at(3, 3, 5))
	if n := strings.Count(r.read(suggest.TaskFile), "## SG-"); n != 2 {
		t.Fatalf("%d tasks", n)
	}
}

func TestFinishedTaskDoesNotBlockARepeat(t *testing.T) {
	r := newRig(t)
	r.res = red("go test ./...", "FAIL TestX\n")
	r.tick(at(2, 3, 5))
	// The developer took the task and removed its pointer; the failure returns.
	if err := os.WriteFile(filepath.Join(r.root, "STATUS-P1.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out := r.tick(at(3, 3, 5))
	if len(out) != 1 || out[0].Pointer == "" {
		t.Fatalf("outcome %+v", out)
	}
	if n := strings.Count(r.read(suggest.TaskFile), "## SG-"); n != 2 {
		t.Fatalf("%d tasks", n)
	}
	if got := strings.TrimSpace(r.read("STATUS-P1.md")); got != out[0].Pointer {
		t.Fatalf("queue %q, pointer %q", got, out[0].Pointer)
	}
}

func TestScheduleTiming(t *testing.T) {
	r := newRig(t)
	if got := r.tick(at(1, 2, 59)); len(got) != 0 {
		t.Fatal("ran before the first slot")
	}
	if got := r.tick(at(2, 3, 0)); len(got) != 1 {
		t.Fatal("did not run at the slot")
	}
	if got := r.tick(at(2, 9, 0)); len(got) != 0 || r.runs != 1 {
		t.Fatalf("ran twice in one day: runs=%d", r.runs)
	}
	if got := r.tick(at(3, 3, 1)); len(got) != 1 || r.runs != 2 {
		t.Fatalf("did not run the next night: runs=%d", r.runs)
	}
}

func TestFirstEnableDoesNotRunImmediately(t *testing.T) {
	r := newRig(t)
	since := at(2, 14, 0) // schedule switched on at 14:00, today's slot is already past
	if got := RunDue(context.Background(), []Job{r.job}, r.deps, since, at(2, 14, 1)); len(got) != 0 || r.runs != 0 {
		t.Fatal("ran at enable time")
	}
	if got := RunDue(context.Background(), []Job{r.job}, r.deps, since, at(3, 3, 0)); len(got) != 1 {
		t.Fatal("did not run at the first slot")
	}
}

func TestMissedNightRunsOnStart(t *testing.T) {
	r := newRig(t)
	r.last = &Last{At: at(1, 3, 0)}
	since := at(3, 9, 0) // the app starts after the slot of the 3rd
	if got := RunDue(context.Background(), []Job{r.job}, r.deps, since, at(3, 9, 1)); len(got) != 1 {
		t.Fatal("missed night was not caught up")
	}
}

func TestNoScheduleNoJobs(t *testing.T) {
	r := newRig(t)
	if got := RunDue(context.Background(), nil, r.deps, at(1, 0, 0), at(5, 12, 0)); len(got) != 0 || r.runs != 0 {
		t.Fatal("ran without jobs")
	}
}

func TestQueueFileOutsideProjectIsNotFiled(t *testing.T) {
	r := newRig(t)
	r.job.QueueFile = "../STATUS.md"
	r.res = red("go test ./...", "FAIL\n")
	out := r.tick(at(2, 3, 5))
	if len(out) != 1 || out[0].Err == nil {
		t.Fatalf("outcome %+v", out)
	}
	if r.read(suggest.TaskFile) != "" {
		t.Fatal("task file written")
	}
	if len(r.marks) != 1 || r.marks[0] || !strings.HasPrefix(r.notes[0], "not filed") {
		t.Fatalf("marks %v notes %v", r.marks, r.notes)
	}
}

func TestRealGatesGreenAndRed(t *testing.T) {
	r := newRig(t)
	r.deps.Run = nil
	r.job.Gates = []string{"exit 0"}
	if o := Execute(context.Background(), r.job, r.deps, at(2, 3, 5)); !o.Passed || o.Pointer != "" {
		t.Fatalf("green: %+v", o)
	}
	r.job.Gates = []string{"echo boom && exit 3"}
	o := Execute(context.Background(), r.job, r.deps, at(3, 3, 5))
	if o.Passed || o.Pointer == "" || !strings.Contains(r.read(suggest.TaskFile), "boom") {
		t.Fatalf("red: %+v\n%s", o, r.read(suggest.TaskFile))
	}
}
