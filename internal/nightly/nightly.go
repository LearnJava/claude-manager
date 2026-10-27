// Package nightly is the scheduled gate run (WASTE-TASKS.md WS-10): at a time
// the project chose it runs the project's gates, and a red result becomes a task
// in the queue with the failing output attached. It never starts a session — the
// queue stays the only way work begins.
package nightly

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/suggest"
	"claude-manager/internal/worker"
)

// Job is one project's schedule, already resolved from its config.
type Job struct {
	Project   string
	Root      string
	Schedule  config.GateSchedule
	Gates     []string
	QueueFile string   // relative to Root; the pointer of a filed task is appended here
	Queues    []string // every queue of the project, to tell an open task from a finished one
}

// Last is when a project's scheduled run last happened.
type Last struct {
	At time.Time
}

// Deps are the outside world of a run; every field but Run is required for
// RunDue, Execute needs Run, Mark and Mu only.
type Deps struct {
	// Last returns the project's latest run, nil = it never ran.
	Last func(project string) (*Last, error)
	// Mark records a finished run. note is "" for green gates, else the pointer
	// of the filed task or the reason none was filed.
	Mark func(project string, at time.Time, passed bool, note string) error
	// Run runs the gates; nil = worker.RunGates.
	Run func(ctx context.Context, root string, gates []string) worker.GateResult
	// Mu, when set, is held while the task file and the queue are written.
	Mu sync.Locker
}

// Outcome is what Execute did for one job.
type Outcome struct {
	Project string
	Passed  bool
	Pointer string // pointer appended to the queue, "" if none
	Err     error  // gates could not be recorded or the task could not be filed
}

// Due reports whether the job should run at now. A project that has run before
// is due when its latest scheduled instant is newer than that run, so a night
// missed while the app was off runs once on the next start. One that never ran
// waits for its first slot after since (the moment the scheduler started), so
// enabling a schedule at noon does not run at noon.
func Due(j Job, last *Last, since, now time.Time) bool {
	slot := j.Schedule.LastDue(now)
	base := since
	if last != nil {
		base = last.At
	}
	return slot.After(base)
}

// RunDue runs, one after another, every job that is due, and returns one
// outcome per job that ran.
func RunDue(ctx context.Context, jobs []Job, d Deps, since, now time.Time) []Outcome {
	var out []Outcome
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		last, err := d.Last(j.Project)
		if err != nil {
			out = append(out, Outcome{Project: j.Project, Err: err})
			continue
		}
		if !Due(j, last, since, now) {
			continue
		}
		out = append(out, Execute(ctx, j, d, now))
	}
	return out
}

// Execute runs the job's gates now. Green gates file nothing. A red run files
// one task unless the same failure is already waiting in a queue.
func Execute(ctx context.Context, j Job, d Deps, now time.Time) Outcome {
	run := d.Run
	if run == nil {
		run = worker.RunGates
	}
	res := run(ctx, j.Root, j.Gates)
	o := Outcome{Project: j.Project, Passed: res.Passed}
	note := ""
	if !res.Passed {
		note, o.Pointer, o.Err = fileTask(j, d.Mu, res, now)
	}
	if err := d.Mark(j.Project, now, res.Passed, note); err != nil && o.Err == nil {
		o.Err = err
	}
	return o
}

// fileTask turns a red gate run into a queue task. It returns the note for the
// record and the pointer appended ("" when the failure is already queued).
func fileTask(j Job, mu sync.Locker, res worker.GateResult, now time.Time) (note, pointer string, err error) {
	s := Suggestion(res, now)
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	ptrs, err := suggest.AcceptUnlessOpen(j.Root, j.QueueFile, j.Queues, []suggest.Suggestion{s})
	if err != nil {
		return "not filed: " + err.Error(), "", err
	}
	if len(ptrs) == 0 {
		return "already queued", "", nil
	}
	return ptrs[0], ptrs[0], nil
}

const (
	outputLimit  = 3000 // bytes of the failing command's output kept in the task
	keyTailBytes = 1500 // bytes of that output the dedup key looks at
)

var (
	// What changes between two runs of the same failure and says nothing about
	// it: timings, timestamps, addresses.
	noiseRe = regexp.MustCompile(`\d+(\.\d+)?\s?(ms|µs|us|ns|s)\b|\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?Z?|0x[0-9a-fA-F]+`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// Suggestion builds the task for a red gate run: the failed command and the tail
// of its output as they were, with a key that stays the same while the failure
// does. Passed runs have no task; callers must not ask.
func Suggestion(res worker.GateResult, now time.Time) suggest.Suggestion {
	cmd, out, exit := "", "", 0
	if f := res.FailedCommand(); f != nil {
		cmd, out, exit = f.Command, f.Output, f.ExitCode
		if f.Err != nil {
			out = strings.TrimSpace(out + "\n" + f.Err.Error())
		}
	}
	out = strings.TrimSpace(out)
	shown := out
	if len(shown) > outputLimit {
		shown = "...[truncated]...\n" + shown[len(shown)-outputLimit:]
	}
	shown = strings.ReplaceAll(shown, "```", "'''")
	detail := fmt.Sprintf("Ночной прогон ворот (%s) закончился неудачей на команде `%s` (код выхода %d). "+
		"Задача заведена автоматически; сессия не запускалась.", now.Format("2006-01-02 15:04"), cmd, exit)
	if shown != "" {
		detail += "\n\nВывод:\n\n```\n" + shown + "\n```"
	}
	return suggest.Suggestion{
		Kind:   suggest.KindNightly,
		Key:    suggest.NewKey(suggest.KindNightly, cmd, normalize(out)),
		Title:  "Починить ворота, упавшие ночью: " + clip(cmd, 60),
		Detail: detail,
		Done:   fmt.Sprintf("Команда `%s` проходит на текущем коммите, остальные ворота проекта тоже.", cmd),
	}
}

// normalize reduces the tail of a failure's output to what identifies it.
func normalize(out string) string {
	if len(out) > keyTailBytes {
		out = out[len(out)-keyTailBytes:]
	}
	return spaceRe.ReplaceAllString(noiseRe.ReplaceAllString(out, ""), " ")
}

func clip(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
