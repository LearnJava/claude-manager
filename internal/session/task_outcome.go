package session

import (
	"context"
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"claude-manager/internal/gitutil"
	"claude-manager/internal/logger"
	"claude-manager/internal/proc"

	_ "modernc.org/sqlite"
)

// Task outcomes of one autonomous run, decided after the CLI process exits
// cleanly. A clean exit only means the TURN ended; whether the TASK ended is a
// separate question, answered from the task queue (the durable git state the
// protocol itself maintains) first and the agent's own continue-session marker
// second — never from the model's prose.
const (
	// OutcomeClosed: the task's pointer left the queue — the task is done.
	OutcomeClosed = "closed"
	// OutcomeSlice: pointer still queued, but the agent ended on the
	// continue-session marker — a finished slice of a multi-slice task.
	OutcomeSlice = "slice"
	// OutcomeUnfinished: pointer still queued and no marker — the run stopped
	// without declaring its work done (step limit, API error, "waiting for a
	// background job" that dies with the process, …).
	OutcomeUnfinished = "unfinished"
)

// Run statuses recorded in session_runs for the two non-closed outcomes; a
// closed task keeps the existing "completed".
const (
	RunStatusSlice      = "slice"
	RunStatusUnfinished = "unfinished"
)

// maxUnfinishedStreak stops the loop after this many consecutive unfinished
// runs of one task: a task that keeps ending without progress (a persistent
// API error, a step budget too small for it) would otherwise be retried
// forever, each retry paying for the context again.
const maxUnfinishedStreak = 3

// classifyTaskOutcome decides the outcome from the queue's top pointer before
// and after the run, and whether the run ended on the continue-session marker.
// before == "" means there is no queue to consult (no task_source, or the file
// could not be read before the run): the marker is then the only evidence, and
// its absence is not held against the run — that keeps sessions without a
// queue (and every existing config) counting tasks exactly as before.
func classifyTaskOutcome(before, after string, marker bool) string {
	if before == "" {
		return OutcomeClosed
	}
	if after != before {
		return OutcomeClosed
	}
	if marker {
		return OutcomeSlice
	}
	return OutcomeUnfinished
}

// queueFetchTimeout bounds the `git fetch` done before reading the queue off
// the integration branch; a slow or unreachable remote falls back to the file
// on disk rather than stalling the run loop.
const queueFetchTimeout = 60 * time.Second

// readTaskQueue returns the task queue's content as the integration branch
// sees it: `origin/<main>:<taskSource>` after a fetch. That is where a
// developer session lands its queue edit (it merges into main from its own
// worktree slot), so the root checkout's copy on disk may lag behind it.
// Falls back to the file on disk when the project has no remote, the file is
// outside the repository, or git fails for any other reason.
func readTaskQueue(ctx context.Context, projectPath, taskSource string) string {
	abs := taskSource
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(projectPath, abs)
	}
	if projectPath != "" {
		if rel, err := filepath.Rel(projectPath, abs); err == nil && !strings.HasPrefix(rel, "..") {
			rel = filepath.ToSlash(rel)
			fctx, cancel := context.WithTimeout(ctx, queueFetchTimeout)
			defer cancel()
			if runGitQuiet(fctx, projectPath, "fetch", "-q", "origin") == nil {
				branch := gitutil.MainBranch(fctx, projectPath)
				if out, err := gitOutput(fctx, projectPath, "show", "origin/"+branch+":"+rel); err == nil {
					return out
				}
			}
		}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	return string(data)
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := execGit(ctx, dir, args...)
	out, err := cmd.Output()
	return string(out), err
}

func runGitQuiet(ctx context.Context, dir string, args ...string) error {
	return execGit(ctx, dir, args...).Run()
}

// ---- Hermes step limit ----

// hermesMaxTurnsDefault replaces the 150-iteration agent.max_turns of the
// user's own Hermes config for manager-driven runs: Claude Code has no such
// cap by default, and a Lumen-sized task was measured at ~130 tool calls
// before it finished (docs/design-decisions.md, "Hermes runtime").
const hermesMaxTurnsDefault = 500

// hermesMaxIterationsRequest is the fixed user message Hermes appends to a
// conversation when its tool-calling budget runs out, asking the model to
// summarize (agent/context_compressor.py MAX_ITERATIONS_SUMMARY_REQUEST).
// `hermes chat --format stream-json` reports nothing else for that case —
// the turn still ends with exit_code 0 — so this row in state.db is the only
// machine-readable signal that the turn was cut off.
const hermesMaxIterationsRequest = "You've reached the maximum number of tool-calling iterations allowed."

// hermesBackgroundNotice prefixes the user-role message Hermes injects when a
// background process the agent started finishes
// (agent/context_compressor.py _BACKGROUND_PROCESS_NOTIFICATION_PREFIX). It
// can land after the step-limit request — measured on a real cut-off Lumen
// run — so it is skipped rather than taken as the turn's last user message.
const hermesBackgroundNotice = "[IMPORTANT: Background process "

// hermesHome resolves Hermes' home directory the way hermes_constants does:
// $HERMES_HOME, else %LOCALAPPDATA%\hermes on Windows, else ~/.hermes; a
// named profile lives under <home>/profiles/<name>.
func hermesHome(profile string) string {
	home := strings.TrimSpace(os.Getenv("HERMES_HOME"))
	if home == "" {
		if la := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); la != "" {
			home = filepath.Join(la, "hermes")
		} else if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".hermes")
		}
	}
	if p := strings.TrimSpace(profile); p != "" && p != "default" {
		home = filepath.Join(home, "profiles", p)
	}
	return home
}

// hermesTurnHitStepLimit reports whether Hermes' conversation convID ended
// its latest turn on the step-limit summary request: the fixed request
// message appears after the last message that is not part of that final
// exchange. Read-only; any failure to open or query state.db reports false
// (the queue check still catches an unfinished task without it).
func hermesTurnHitStepLimit(dbPath, convID string) bool {
	if convID == "" || dbPath == "" {
		return false
	}
	if _, err := os.Stat(dbPath); err != nil {
		return false
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return false
	}
	defer db.Close()
	// Only the tail of the conversation matters: the request is followed by
	// the model's summary, plus any background-process notices and the
	// replies to them.
	rows, err := db.Query(
		`SELECT role, content FROM messages WHERE session_id = ? ORDER BY id DESC LIMIT 12`, convID)
	if err != nil {
		logger.L.Debug("session.hermes.state_db_query", "error", err)
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		var content sql.NullString
		if rows.Scan(&role, &content) != nil {
			return false
		}
		if role != "user" || strings.HasPrefix(content.String, hermesBackgroundNotice) {
			continue
		}
		return strings.Contains(content.String, hermesMaxIterationsRequest)
	}
	return false
}

// hermesStateDB is the state.db of the Hermes profile this session runs in.
func (s *Session) hermesStateDB() string {
	return filepath.Join(hermesHome(s.Config.HermesProfile), "state.db")
}

// resumeHermesAfterFailure makes the next run of a Hermes session continue
// the conversation the failed run was in (via resumeSessionID, which
// runOnceHermes passes as --resume), rather than starting the task over in a
// fresh conversation that has to re-read everything. Claude sessions keep
// their existing behaviour.
func (s *Session) resumeHermesAfterFailure() {
	if !s.Config.IsHermes() {
		return
	}
	s.mu.Lock()
	if s.lastHermesConv != "" {
		s.resumeSessionID = s.lastHermesConv
	}
	s.mu.Unlock()
}

// hermesAgentLog is the agent.log of the Hermes profile this session runs in.
func (s *Session) hermesAgentLog() string {
	return filepath.Join(hermesHome(s.Config.HermesProfile), "logs", "agent.log")
}

// hermesLogTail bounds how much of agent.log is scanned: one turn's lines
// are at its end, and the file grows by megabytes a day.
const hermesLogTail = 4 << 20

// hermesTurnHitRateLimit reports whether Hermes' agent.log shows a 429 for
// conversation convID at or after since. `hermes chat` reports only the
// turn's LAST error, and Hermes rotates credentials on a 429 — so a spent
// usage limit can surface as some other credential's error (measured on S6:
// 429 on the working token, then "401 OAuth access token has been revoked"
// from the stale fallback token, and the manager saw only the 401). The
// per-conversation log lines are the one place the 429 survives.
func hermesTurnHitRateLimit(logPath, convID string, since time.Time) bool {
	if logPath == "" || convID == "" {
		return false
	}
	f, err := os.Open(logPath)
	if err != nil {
		return false
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > hermesLogTail {
		if _, err := f.Seek(st.Size()-hermesLogTail, 0); err != nil {
			return false
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return false
	}
	tag := "[" + convID + "]"
	// Log timestamps are local wall-clock "2006-01-02 15:04:05"; compare as
	// strings in that same layout (second precision is enough here).
	cutoff := since.Add(-time.Second).Format("2006-01-02 15:04:05")
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, tag) || len(line) < 19 || line[:19] < cutoff {
			continue
		}
		if strings.Contains(line, "RateLimitError") || strings.Contains(line, "Credential 429") ||
			strings.Contains(line, "rate_limit_error") {
			return true
		}
	}
	return false
}

// execGit builds a git command in dir with its console window hidden, like
// gitutil's own runGit.
func execGit(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	proc.HideConsole(cmd)
	cmd.Dir = dir
	return cmd
}
