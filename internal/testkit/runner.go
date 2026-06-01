package testkit

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"time"
)

// lineResult holds one line read from stdin (or an error).
type lineResult struct {
	line string
	err  error
}

// Runner executes a Scenario's steps against stdin/stdout.
type Runner struct {
	scenario *Scenario
	vars     Vars
	speed    float64
	out      io.Writer
	stored   map[string]map[string]interface{}
	lineCh   chan lineResult
}

// NewRunner creates a Runner and starts a background goroutine that reads
// lines from in into lineCh. The FAKECLAUDE_SPEED env var scales timing
// (default 1.0; higher values divide delays so tests run faster).
func NewRunner(s *Scenario, vars Vars, in io.Reader, out io.Writer) *Runner {
	speed := 1.0
	if sv := os.Getenv("FAKECLAUDE_SPEED"); sv != "" {
		if f, err := strconv.ParseFloat(sv, 64); err == nil && f > 0 {
			speed = f
		}
	}

	r := &Runner{
		scenario: s,
		vars:     vars,
		speed:    speed,
		out:      out,
		stored:   make(map[string]map[string]interface{}),
		lineCh:   make(chan lineResult, 64),
	}

	go func() {
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			r.lineCh <- lineResult{line: scanner.Text()}
		}
		if err := scanner.Err(); err != nil {
			r.lineCh <- lineResult{err: err}
		}
		close(r.lineCh)
	}()

	return r
}

// Run executes all steps in order and returns the scenario's ExitCode.
func (r *Runner) Run() (int, error) {
	for i := range r.scenario.Steps {
		step := r.scenario.Steps[i]

		// Check branching conditions; skip step if they don't match.
		if !r.checkOn(step.On) {
			continue
		}

		var err error
		switch step.Type {
		case "emit":
			err = r.execEmit(step)
		case "await_stdin":
			err = r.execAwaitStdin(step)
		}
		if err != nil {
			return 1, err
		}
	}
	return r.scenario.ExitCode, nil
}

// checkOn returns true when all conditions in on are satisfied by stored values.
// An empty or nil on map always returns true.
// Key format: "stored_name.field" (e.g. "perm.decision").
func (r *Runner) checkOn(on map[string]string) bool {
	if len(on) == 0 {
		return true
	}
	for key, want := range on {
		dot := -1
		for i := 0; i < len(key); i++ {
			if key[i] == '.' {
				dot = i
				break
			}
		}
		if dot < 0 {
			return false
		}
		storeName := key[:dot]
		field := key[dot+1:]

		stored, ok := r.stored[storeName]
		if !ok {
			return false
		}
		got, ok := stored[field]
		if !ok {
			return false
		}
		if got != want {
			return false
		}
	}
	return true
}

// execEmit sleeps delay_ms/speed ms, then writes vars.Apply(step.Event) + newline to out.
func (r *Runner) execEmit(step Step) error {
	if step.DelayMs > 0 {
		d := time.Duration(float64(step.DelayMs)/r.speed) * time.Millisecond
		time.Sleep(d)
	}
	line := r.vars.Apply(step.Event)
	_, err := r.out.Write(append(line, '\n'))
	return err
}

// execAwaitStdin reads from lineCh with a timeout (default 30 s / speed).
// It skips non-JSON lines and, when Expect is set, skips lines whose "type"
// doesn't match. If StoreAs is set, the parsed map is stored in r.stored.
func (r *Runner) execAwaitStdin(step Step) error {
	timeoutMs := step.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30_000
	}
	d := time.Duration(float64(timeoutMs)/r.speed) * time.Millisecond
	deadline := time.NewTimer(d)
	defer deadline.Stop()

	for {
		select {
		case res, ok := <-r.lineCh:
			if !ok {
				// Channel closed — stdin EOF.
				return io.EOF
			}
			if res.err != nil {
				return res.err
			}
			line := res.line
			if line == "" {
				continue
			}
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				// Not JSON — skip.
				continue
			}
			// If Expect is set, check the "type" field.
			if step.Expect != "" {
				if t, _ := m["type"].(string); t != step.Expect {
					continue
				}
			}
			// Store the parsed map if requested.
			if step.StoreAs != "" {
				r.stored[step.StoreAs] = m
			}
			return nil

		case <-deadline.C:
			return nil // Timeout — don't treat as fatal; just proceed.
		}
	}
}
