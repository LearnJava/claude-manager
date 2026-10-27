package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
	"claude-manager/internal/nightly"
	"claude-manager/internal/store"
)

// ---- Nightly gate run (internal/nightly, WASTE-TASKS.md WS-10) ----
//
// A project with `gate_schedule = "HH:MM"` gets its gates run in the project
// folder at that time; a red result becomes a task in the queue with the error
// attached. The loop only ever files a task — it never starts a session.

// nightlyTick is how often the loop looks at the clock; a schedule has minute
// resolution.
const nightlyTick = time.Minute

// nightlyTimeout bounds one project's gate run.
const nightlyTimeout = 30 * time.Minute

// nightlyJobs resolves the schedules of cfg's projects. A project with an
// empty schedule is not scheduled; one that cannot run (no gates, no queue) is
// skipped, and why is logged once per reason.
func nightlyJobs(cfg *config.AppConfig, warned map[string]bool) []nightly.Job {
	if cfg == nil {
		return nil
	}
	var jobs []nightly.Job
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if strings.TrimSpace(p.GateSchedule) == "" || p.Path == "" {
			continue
		}
		sched, err := config.ParseGateSchedule(p.GateSchedule)
		if err != nil {
			continue // validate() rejects it on load; nothing to run
		}
		gates := p.GateScheduleGates
		if len(gates) == 0 {
			gates = p.Gates
		}
		queues := projectQueueFiles(p)
		queue := strings.TrimSpace(p.GateScheduleQueue)
		if queue == "" && len(queues) > 0 {
			queue = queues[0]
		}
		reason := ""
		switch {
		case len(gates) == 0:
			reason = "no gates to run (set gate_schedule_gates or gates)"
		case queue == "":
			reason = "no queue to file a task into (set gate_schedule_queue or a session task_source)"
		}
		if reason != "" {
			if !warned[p.Name+reason] {
				warned[p.Name+reason] = true
				logger.L.Warn("nightly.skip", "project", p.Name, "reason", reason)
			}
			continue
		}
		jobs = append(jobs, nightly.Job{Project: p.Name, Root: p.Path, Schedule: sched,
			Gates: gates, QueueFile: queue, Queues: queues})
	}
	return jobs
}

// startNightlyLoop checks the clock every minute and runs the gates of every
// project whose slot has come. Runs of different projects go in parallel; a
// project is never run twice at once. Without a store nothing is recorded and
// the loop does not start.
func (a *App) startNightlyLoop(ctx context.Context) {
	if a.store == nil {
		return
	}
	st := a.store
	since := time.Now()
	var mu sync.Mutex
	running := map[string]bool{}
	warned := map[string]bool{}
	deps := nightly.Deps{
		Last: func(project string) (*nightly.Last, error) {
			r, err := st.LastNightlyRun(project)
			if err != nil || r == nil {
				return nil, err
			}
			return &nightly.Last{At: r.TS}, nil
		},
		Mark: func(project string, at time.Time, passed bool, note string) error {
			return st.SetNightlyRun(store.NightlyRun{Project: project, TS: at.UTC(), Passed: passed, Note: note})
		},
		Mu: &suggestMu,
	}
	go func() {
		defer logger.Recover("nightly.loop")
		t := time.NewTicker(nightlyTick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			now := time.Now()
			for _, j := range nightlyJobs(a.cfg, warned) {
				last, err := deps.Last(j.Project)
				if err != nil || !nightly.Due(j, last, since, now) {
					continue
				}
				mu.Lock()
				busy := running[j.Project]
				running[j.Project] = true
				mu.Unlock()
				if busy {
					continue
				}
				go func(j nightly.Job) {
					defer logger.Recover("nightly.run")
					defer func() { mu.Lock(); delete(running, j.Project); mu.Unlock() }()
					rctx, cancel := context.WithTimeout(ctx, nightlyTimeout)
					defer cancel()
					o := nightly.Execute(rctx, j, deps, time.Now())
					logger.L.Info("nightly.run", "project", o.Project, "passed", o.Passed, "task", o.Pointer)
					if o.Err != nil {
						logger.L.Warn("nightly.run_error", "project", o.Project, "error", o.Err)
					}
				}(j)
			}
		}
	}()
}
