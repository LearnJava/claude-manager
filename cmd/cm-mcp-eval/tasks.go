package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

// Task is one thing a real agent is asked to do with only cm-mcp. The outcome
// is judged by the manager's state, not by what the agent says; for read-only
// tasks the agent reports a value, which is compared with the one the state
// holds.
type Task struct {
	ID    string
	Area  string // the MCP-06..11 area it exercises
	Level string // cm-mcp access level the agent gets
	// Setup prepares the instance and returns the task text for the agent.
	Setup func(in *Instance) (string, error)
	// Check judges the outcome; answer is the agent's ANSWER line ("" if none).
	Check func(in *Instance, answer string) error
}

const settle = 20 * time.Second

func sess(name, prompt string) config.SessionConfig {
	return config.SessionConfig{Name: name, Prompt: prompt, Model: "claude-sonnet-4-6", PermissionMode: "default"}
}

// startAll starts sessions of project and waits until each reaches status.
func startAll(in *Instance, project, status string, names ...string) error {
	for _, n := range names {
		if err := startOne(in, project, n); err != nil {
			return err
		}
	}
	for _, n := range names {
		if err := in.WaitStatus(project+"/"+n, status, settle); err != nil {
			return err
		}
	}
	return nil
}

// startOne starts a session and waits until its run row is stored. Sessions
// started in the same instant can lose their run rows (the log of a failed one
// is then unreachable), so the setup starts them one after another.
func startOne(in *Instance, project, name string) error {
	if err := in.Mgr.StartSession(project, name); err != nil {
		return err
	}
	for end := time.Now().Add(settle); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if runs, _ := in.Store.ListRuns(project, name, 1); len(runs) > 0 {
			return nil
		}
	}
	return fmt.Errorf("run of %s/%s never stored", project, name)
}

func runCount(in *Instance, project string) int {
	runs, _ := in.Mgr.GetHistory(project, 50)
	return len(runs)
}

func logText(in *Instance, id string) string {
	entries, _ := in.Mgr.GetSessionLog(id, 0, 500)
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Message)
		b.WriteByte('\n')
	}
	return b.String()
}

func expect(cond bool, format string, args ...any) error {
	if cond {
		return nil
	}
	return fmt.Errorf(format, args...)
}

func tasks() []Task {
	return []Task{
		{
			ID: "setup-from-scratch", Area: "sessions+config", Level: "admin",
			Setup: func(in *Instance) (string, error) {
				dir := filepath.Join(in.Dir, "notes-folder")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return "", err
				}
				return fmt.Sprintf("Register the folder %s as a project named notes. Add a session Writer to it with the prompt "+
					"\"simple hello task\", model claude-sonnet-4-6 and permission mode acceptEdits. Start the session and "+
					"wait until it has finished its turn.", dir), nil
			},
			Check: func(in *Instance, _ string) error {
				p, ok := in.Project("notes")
				if !ok || !strings.HasSuffix(filepath.ToSlash(p.Path), "notes-folder") {
					return fmt.Errorf("project notes missing or on the wrong folder: %+v", p)
				}
				s, ok := in.Session("notes", "Writer")
				if !ok {
					return fmt.Errorf("session Writer missing")
				}
				if s.Prompt != "simple hello task" || s.Model != "claude-sonnet-4-6" || s.PermissionMode != "acceptEdits" {
					return fmt.Errorf("session Writer misconfigured: %+v", s)
				}
				if err := expect(runCount(in, "notes") >= 1, "Writer never ran"); err != nil {
					return err
				}
				return in.WaitStatus("notes/Writer", "idle", settle)
			},
		},
		{
			ID: "run-and-report-cost", Area: "sessions+metrics", Level: "operate",
			Setup: func(in *Instance) (string, error) {
				_, err := in.AddProject("shop", sess("Build", "simple hello task"))
				return "Run the session Build of project shop to the end of its turn. Then tell me what that session cost in USD.", err
			},
			Check: func(in *Instance, answer string) error {
				m, err := in.Mgr.GetSessionMetrics("shop/Build")
				if err != nil || m.TotalCostUSD == 0 {
					return fmt.Errorf("Build did not run (cost %v, err %v)", m.TotalCostUSD, err)
				}
				return numberNear(answer, m.TotalCostUSD)
			},
		},
		{
			ID: "answer-question", Area: "interaction", Level: "operate",
			Setup: func(in *Instance) (string, error) {
				s := sess("Planner", "ask-user-marker: decide the latency budget")
				s.AskUserMarker = true
				if _, err := in.AddProject("plan", s); err != nil {
					return "", err
				}
				if err := startAll(in, "plan", "waiting_for_user", "Planner"); err != nil {
					return "", err
				}
				return "The session Planner of project plan asked a question and waits. Answer it by choosing the option that relaxes the budget.", nil
			},
			Check: func(in *Instance, _ string) error {
				if q := in.Mgr.GetPendingQuestions(); len(q) != 0 {
					return fmt.Errorf("question still pending: %+v", q)
				}
				if err := in.WaitStatus("plan/Planner", "idle", settle); err != nil {
					return err
				}
				return expect(strings.Contains(logText(in, "plan/Planner"), "Relax to 16ms"), "the chosen option never reached the session")
			},
		},
		{
			ID: "approve-one-permission", Area: "interaction", Level: "admin",
			Setup: func(in *Instance) (string, error) {
				if _, err := in.AddProject("ops", sess("A", "refactor module a"), sess("B", "delete the build output")); err != nil {
					return "", err
				}
				if err := startAll(in, "ops", "waiting_permission", "A", "B"); err != nil {
					return "", err
				}
				return "Sessions A and B of project ops each wait for a permission. Approve the request that edits a source file and deny the one that deletes build artifacts.", nil
			},
			Check: func(in *Instance, _ string) error {
				if p := in.Mgr.GetPendingPermissions(); len(p) != 0 {
					return fmt.Errorf("permissions still pending: %+v", p)
				}
				evs, _ := in.Store.PermissionEventsForProject("ops")
				got := map[string]string{}
				for _, e := range evs {
					got[e.Session] = strings.ToLower(e.Decision)
				}
				if !strings.HasPrefix(got["A"], "allow") {
					return fmt.Errorf("the source edit (A) was not approved: %v", got)
				}
				if !strings.HasPrefix(got["B"], "den") {
					return fmt.Errorf("the deletion (B) was not denied: %v", got)
				}
				return nil
			},
		},
		{
			ID: "switch-model-and-restart", Area: "sessions", Level: "operate",
			Setup: func(in *Instance) (string, error) {
				if _, err := in.AddProject("dev", sess("Worker", "mcp-full-control: start")); err != nil {
					return "", err
				}
				if err := startAll(in, "dev", "waiting_for_user", "Worker"); err != nil {
					return "", err
				}
				return "Switch the session Worker of project dev to the model claude-opus-4-6 and restart it so it runs on that model.", nil
			},
			Check: func(in *Instance, _ string) error {
				s, _ := in.Session("dev", "Worker")
				if s.Model != "claude-opus-4-6" {
					return fmt.Errorf("configured model is %q", s.Model)
				}
				if err := expect(runCount(in, "dev") >= 2, "Worker was not restarted (runs: %d)", runCount(in, "dev")); err != nil {
					return err
				}
				st, _ := in.Mgr.GetSession("dev/Worker")
				return expect(strings.Contains(st.Model, "opus"), "live model is %q", st.Model)
			},
		},
		{
			ID: "stop-project", Area: "sessions", Level: "admin",
			Setup: func(in *Instance) (string, error) {
				if _, err := in.AddProject("dock", sess("X", "mcp-full-control: x"), sess("Y", "mcp-full-control: y")); err != nil {
					return "", err
				}
				if err := startAll(in, "dock", "waiting_for_user", "X", "Y"); err != nil {
					return "", err
				}
				return "Stop every running session of project dock. Do not touch other projects.", nil
			},
			Check: func(in *Instance, _ string) error {
				for _, n := range []string{"X", "Y"} {
					if err := in.WaitStatus("dock/"+n, "idle", settle); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			ID: "update-session-config", Area: "projects+config", Level: "admin",
			Setup: func(in *Instance) (string, error) {
				_, err := in.AddProject("ops2", sess("Nightly", "simple hello task"))
				return "In project ops2 change the session Nightly: model claude-haiku-4-5, permission mode acceptEdits, " +
					"budget limit 2.5 USD. Leave everything else as it is.", err
			},
			Check: func(in *Instance, _ string) error {
				s, _ := in.Session("ops2", "Nightly")
				if s.Model != "claude-haiku-4-5" || s.PermissionMode != "acceptEdits" || s.MaxBudgetUSD != 2.5 {
					return fmt.Errorf("Nightly not updated: %+v", s)
				}
				return expect(s.Prompt == "simple hello task", "prompt was lost: %q", s.Prompt)
			},
		},
		{
			ID: "remove-project", Area: "projects+config", Level: "admin",
			Setup: func(in *Instance) (string, error) {
				if _, err := in.AddProject("old", sess("S", "x")); err != nil {
					return "", err
				}
				_, err := in.AddProject("keep", sess("S", "x"))
				return "Remove the project old from the manager's registry. The project keep must stay.", err
			},
			Check: func(in *Instance, _ string) error {
				if _, ok := in.Project("old"); ok {
					return fmt.Errorf("project old still registered")
				}
				_, ok := in.Project("keep")
				return expect(ok, "project keep is gone")
			},
		},
		{
			ID: "find-failed-session", Area: "logs", Level: "read",
			Setup: func(in *Instance) (string, error) {
				if _, err := in.AddProject("ci", sess("Lint", "simple hello task"), sess("Test", "eval-failing run"), sess("Docs", "simple hello docs")); err != nil {
					return "", err
				}
				for _, n := range []string{"Lint", "Test", "Docs"} {
					if err := startOne(in, "ci", n); err != nil {
						return "", err
					}
				}
				for end := time.Now().Add(settle); time.Now().Before(end) && !strings.Contains(logText(in, "ci/Test"), "users_email_key"); {
					time.Sleep(100 * time.Millisecond)
				}
				time.Sleep(time.Second)
				return "One session of project ci ended with an error. Find it and read why. Reply with ANSWER: <session name>/<name of the violated constraint>.", nil
			},
			Check: func(_ *Instance, answer string) error {
				a := strings.ToLower(answer)
				return expect(strings.Contains(a, "test") && strings.Contains(a, "users_email_key") && !strings.Contains(a, "lint"),
					"answer %q should name session Test and constraint users_email_key", answer)
			},
		},
		{
			ID: "project-cost", Area: "metrics", Level: "read",
			Setup: func(in *Instance) (string, error) {
				if _, err := in.AddProject("fin", sess("One", "simple hello one"), sess("Two", "simple hello two")); err != nil {
					return "", err
				}
				if err := startAll(in, "fin", "idle", "One", "Two"); err != nil {
					return "", err
				}
				time.Sleep(time.Second)
				return "How much did project fin cost in total over the last 7 days, in USD? Reply with ANSWER: <number>.", nil
			},
			Check: func(in *Instance, answer string) error {
				c, err := in.Mgr.GetProjectCost("fin", 7)
				if err != nil || c == 0 {
					return fmt.Errorf("no cost recorded (%v, %v)", c, err)
				}
				return numberNear(answer, c)
			},
		},
		{
			ID: "generate-and-approve-roadmap", Area: "planning", Level: "admin",
			Setup: func(in *Instance) (string, error) {
				_, err := in.AddProject("books", sess("Main", "simple hello task"))
				return "For project books generate a roadmap from the idea \"eval-roadmap: a bookmark manager\", approve it, " +
					"and then read the roadmap of the session P1. Reply with ANSWER: <number of tasks in it>.", err
			},
			Check: func(in *Instance, answer string) error {
				p, _ := in.Project("books")
				for _, f := range []string{"ROADMAP.md", "STATUS-P1.md"} {
					if _, err := os.Stat(filepath.Join(p.Path, f)); err != nil {
						return fmt.Errorf("%s was not written: %v", f, err)
					}
				}
				s, ok := in.Session("books", "P1")
				if !ok || s.TaskSource != "STATUS-P1.md" {
					return fmt.Errorf("session P1 missing or without its queue: %+v", s)
				}
				return expect(strings.TrimSpace(answer) == "3", "answer %q, want 3", answer)
			},
		},
		{
			ID: "skill-candidates", Area: "skills", Level: "read",
			Setup: func(in *Instance) (string, error) {
				if _, err := in.AddProject("sk", sess("S", "x")); err != nil {
					return "", err
				}
				if err := seedActions(in.Store, "sk"); err != nil {
					return "", err
				}
				return "Which sequence of tool calls in project sk is the best candidate for a new skill? Reply with " +
					"ANSWER: <the signatures of the sequence, in order, joined with ' > '>.", nil
			},
			Check: func(in *Instance, answer string) error {
				cands, err := experience.MineProjectCandidates(in.Store, "sk")
				if err != nil || len(cands) == 0 {
					return fmt.Errorf("no candidates (%v)", err)
				}
				best := cands[0]
				for _, c := range cands {
					if c.Score > best.Score {
						best = c
					}
				}
				last := -1
				for _, sig := range best.Sig {
					i := strings.Index(answer[max(last, 0):], sig)
					if i < 0 {
						return fmt.Errorf("answer %q lacks %q of the best candidate %v in order", answer, sig, best.Sig)
					}
					last = max(last, 0) + i + len(sig)
				}
				return nil
			},
		},
		{
			ID: "wait-for-permission", Area: "events", Level: "operate",
			Setup: func(in *Instance) (string, error) {
				_, err := in.AddProject("wa", sess("Alpha", "refactor module"), sess("Beta", "simple hello task"))
				return "Start both sessions of project wa. Wait until one of them needs a permission and tell me which tool it asks for " +
					"and which session it is. Reply with ANSWER: <session>/<tool>. Do not decide on the permission.", err
			},
			Check: func(in *Instance, answer string) error {
				p := in.Mgr.GetPendingPermissions()
				if len(p) != 1 {
					return fmt.Errorf("want the permission left pending, got %+v", p)
				}
				return expect(strings.EqualFold(strings.TrimSpace(answer), "Alpha/"+p[0].Tool),
					"answer %q, want Alpha/%s", answer, p[0].Tool)
			},
		},
	}
}

// seedActions writes a tool-call sequence recurring over three runs plus noise,
// the shape experience.MineProjectCandidates turns into a skill candidate.
func seedActions(st *store.Store, project string) error {
	now := time.Now().UTC()
	var rows []store.ActionRow
	for i := 0; i < 4; i++ {
		r := &store.SessionRun{Project: project, Session: "S", Model: "sonnet", StartedAt: now, Status: "completed"}
		if err := st.InsertRun(r); err != nil {
			return err
		}
		rows = append(rows,
			store.ActionRow{Project: project, Session: "S", RunID: &r.ID, StepIndex: 0, Tool: "Bash", Sig: "Bash:git pull", Arg: "git pull", ResultChars: 400, Timestamp: now},
			store.ActionRow{Project: project, Session: "S", RunID: &r.ID, StepIndex: 1, Tool: "Bash", Sig: "Bash:go test <ARG>", Arg: "go test ./...", ResultChars: 900, Timestamp: now},
			store.ActionRow{Project: project, Session: "S", RunID: &r.ID, StepIndex: 2, Tool: "Bash", Sig: "Bash:git add <ARG>", Arg: "git add -A", Timestamp: now},
		)
		if i == 0 {
			rows = append(rows, store.ActionRow{Project: project, Session: "S", RunID: &r.ID, StepIndex: 3, Tool: "Read", Sig: "Read:<FILE>", Arg: "a.go", Timestamp: now})
		}
	}
	return st.InsertActions(rows)
}

// numberNear compares a reported number with the one in the state.
func numberNear(answer string, want float64) error {
	f := strings.Fields(strings.NewReplacer("$", " ", "USD", " ", "usd", " ").Replace(answer))
	if len(f) == 0 {
		return fmt.Errorf("no number in the answer %q", answer)
	}
	got, err := strconv.ParseFloat(strings.TrimRight(f[0], ".,"), 64)
	if err != nil {
		return fmt.Errorf("answer %q is not a number", answer)
	}
	return expect(math.Abs(got-want) < 0.005, "answer %v, state says %v", got, want)
}
