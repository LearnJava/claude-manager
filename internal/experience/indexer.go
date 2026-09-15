package experience

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"claude-manager/internal/store"
)

// ImportOpts configures one IngestDir pass.
type ImportOpts struct {
	// OnProgress, if set, is called after each file is processed (whether
	// imported, skipped, or errored) — a bulk import over thousands of files
	// otherwise looks like a hung UI (LEARN-TASKS.md LN-17).
	OnProgress func(processed, total int)
}

// ImportStats summarizes one IngestDir pass.
type ImportStats struct {
	Files   int // log files newly parsed and ingested
	Runs    int // of Files, how many actually produced at least one step
	Actions int // action_signatures rows inserted (tool_use steps with a tool)
	Skipped int // files already imported (matched by name+size+mtime) — a no-op
	Errors  int // unreadable/unparseable files, plus malformed lines tolerated
	// inside an otherwise-parsed file (Trajectory.Skipped)
}

// IngestDir recursively walks root, bulk-importing every CLI JSONL transcript
// (LN-01) and auto-saved markdown session log (LN-17) it finds into project's
// action_signatures rows. Rows are written with RunID == nil (no matching
// session_runs row for a bulk-imported file) and CLISessionID set to the
// file's base name — the only stable per-file identity a markdown log
// carries (LEARN-TASKS.md LN-17).
//
// Already-imported files are skipped by (name, size, mtime)
// (store.IsLogFileImported) rather than the byte-offset checkpoint
// ingest_state uses for a live, growing transcript: a saved log file is
// closed and complete the moment it exists, so "already imported or not" is
// the only question, and re-running IngestDir over the same directory (or a
// directory it's a superset of) is then a no-op.
//
// projectPath is the local project's own config.ProjectConfig.Path, used as
// the fallback for actionRows' signature normalization when a file's own
// Trajectory.ProjectPath is empty — true for every markdown log (LN-17),
// which carries no cwd (LEARN-TASKS.md LN-19). It is not guessed from root:
// the log directory being walked may have been brought from another machine
// entirely, so its paths may not match projectPath at all — see
// sanitizeForeignPath in signature.go for that case.
func IngestDir(st *store.Store, root, project, projectPath string, opts ImportOpts) (ImportStats, error) {
	var stats ImportStats
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort walk; unreadable entries are just skipped
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".jsonl", ".md":
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return stats, err
	}

	for i, path := range files {
		ingestOne(st, path, project, projectPath, &stats)
		if opts.OnProgress != nil {
			opts.OnProgress(i+1, len(files))
		}
	}
	return stats, nil
}

// ingestOne imports a single file, updating stats in place. Every failure
// mode (unreadable file, unparseable content, a failed insert) counts as an
// error and moves on to the next file — one bad file must never abort the
// whole directory (LEARN-TASKS.md LN-17: "файл с битой записью
// импортируется частично").
func ingestOne(st *store.Store, path, project, projectPath string, stats *ImportStats) {
	info, err := os.Stat(path)
	if err != nil {
		stats.Errors++
		return
	}
	name := filepath.Base(path)

	imported, err := st.IsLogFileImported(project, name, info.Size(), info.ModTime())
	if err != nil {
		stats.Errors++
		return
	}
	if imported {
		stats.Skipped++
		return
	}

	var traj Trajectory
	if strings.ToLower(filepath.Ext(path)) == ".md" {
		traj, err = ParseLogFile(path)
	} else {
		traj, err = Read(path)
	}
	if err != nil {
		stats.Errors++
		return
	}
	// A malformed line inside an otherwise-readable file is not fatal — the
	// rest of the trajectory still gets imported — but it is still a defect
	// worth surfacing in the summary.
	stats.Errors += traj.Skipped

	rows := actionRows(traj, project, traj.SessionID, projectPath, nil, name, "")
	if len(rows) > 0 {
		if err := st.InsertActions(rows); err != nil {
			stats.Errors++
			return
		}
	}
	if err := st.MarkLogFileImported(project, name, info.Size(), info.ModTime()); err != nil {
		stats.Errors++
		return
	}

	stats.Files++
	if len(traj.Steps) > 0 {
		stats.Runs++
	}
	stats.Actions += len(rows)
}

// actionRows converts a Trajectory's tool_use steps into store.ActionRow
// values ready for InsertActions — shared by the bulk import above (LN-17:
// runID nil, cliSessionID the file name) and IngestRun below (LN-03: a real
// runID when the finished run has a session_runs row, the CLI's own session
// id, and the resolved task pointer).
//
// fallbackProjectPath is used for signature normalization only when
// traj.ProjectPath is empty — always true for a markdown-log Trajectory
// (LEARN-TASKS.md LN-19), which carries no cwd; traj.ProjectPath still wins
// whenever it is set, since it reflects the actual machine/run the steps
// came from.
func actionRows(traj Trajectory, project, sessionName, fallbackProjectPath string, runID *int64, cliSessionID, taskPtr string) []store.ActionRow {
	projectPath := traj.ProjectPath
	if projectPath == "" {
		projectPath = fallbackProjectPath
	}
	var rows []store.ActionRow
	for _, step := range traj.Steps {
		if step.Kind != StepToolUse || step.ToolName == "" {
			continue
		}
		sig, arg := Signature(step.ToolName, step.InputText, projectPath)
		var outTokens int64
		if step.Usage != nil {
			outTokens = int64(step.Usage.OutputTokens)
		}
		rows = append(rows, store.ActionRow{
			Project:      project,
			Session:      sessionName,
			RunID:        runID,
			CLISessionID: cliSessionID,
			TaskPtr:      taskPtr,
			StepIndex:    step.Index,
			Tool:         step.ToolName,
			Sig:          sig,
			Arg:          arg,
			IsError:      step.ResultIsError,
			OutTokens:    outTokens,
			ResultChars:  step.ResultChars,
			DurSec:       stepDurSec(step),
			Timestamp:    step.Time,
		})
	}
	return rows
}

// IngestResult reports the outcome of one IngestRun call — LEARN-TASKS.md
// LN-21's fix for the two outcomes that used to look identical from the
// caller's side ("nothing happened"): a run with genuinely nothing to index
// (Reason set, err nil) versus one IngestRun actually failed to index
// (err set). Rows is 0 in both cases, but only the latter is worth alerting
// on.
type IngestResult struct {
	Rows   int
	Reason string // "" on an ordinary transcript-backed ingest; see Reason* consts otherwise
}

// Reason values for IngestResult.Reason (LEARN-TASKS.md LN-21).
const (
	// ReasonNoCLISessionID: the run never got a system/init event (e.g. the
	// process died immediately), so there is no CLI session id and thus no
	// transcript — or markdown log — to find. Not an error: this is expected
	// for a fraction of error-status runs.
	ReasonNoCLISessionID = "no_cli_session_id"
	// ReasonFallbackMD: the CLI's own JSONL transcript could not be found
	// (cleaned up, or the machine's ~/.claude/projects/ never had it), so
	// this run's auto-saved markdown log was parsed instead.
	ReasonFallbackMD = "fallback_md"
	// ReasonAlreadyIngestedMD: a repeat call after ReasonFallbackMD — the
	// exact same markdown log was already consumed, so this is an idempotent
	// no-op rather than a fresh (possibly duplicating) parse.
	ReasonAlreadyIngestedMD = "already_ingested_md"
)

// IngestRun indexes one finished run's newly-appended CLI transcript lines
// into action_signatures (LEARN-TASKS.md LN-03), called from
// SessionManager.finishRun when experience_tracking is on. It is idempotent
// via the per-CLI-session byte offset in ingest_state
// (GetIngestOffset/SetIngestOffset, LN-02): re-running it for the same
// cliSessionID only reads what wasn't read last time, so a retried or
// duplicate call never re-inserts rows.
//
// A live run's transcript (unlike a closed, bulk-imported log file) is the
// one place a real run_id is available, so rows carry it — TopSignatures'
// DistinctRuns then counts these rows by run_id directly, falling back to
// cli_session_id only for the bulk-imported rows that have none.
//
// mdLogPath, when non-empty, is this same run's own auto-saved markdown log
// (store.SaveSessionLogFile, written by the same finishRun call that invokes
// this) — the fallback source when the CLI's own JSONL transcript cannot be
// found (LEARN-TASKS.md LN-21: a real-corpus measurement found this to be the
// dominant cause of near-zero live-ingest coverage on a machine where old
// transcripts get cleaned up or relocated under a worktree-specific project
// slug). Unlike a JSONL transcript, a markdown log carries no byte offset to
// resume from (ParseLogFile always parses the whole file, LN-17) — so the
// saved ingest_state row for this fallback stores the md file's own path and
// size instead of a transcript byte offset, purely so a repeat call can tell
// "already consumed this exact file" and skip re-parsing rather than
// duplicating rows.
func IngestRun(st *store.Store, project, sessionName string, runID int64, cliSessionID, projectPath, taskPtr, mdLogPath string) (IngestResult, error) {
	if cliSessionID == "" {
		return IngestResult{Reason: ReasonNoCLISessionID}, nil
	}

	savedPath, savedOffset, ok, err := st.GetIngestOffset(cliSessionID)
	if err != nil {
		return IngestResult{}, err
	}

	var rid *int64
	if runID != 0 {
		rid = &runID
	}

	path, findErr := FindTranscript(TranscriptsRoot(), projectPath, cliSessionID)
	if findErr == nil {
		// The saved offset only means something against the exact file it
		// was recorded for — a transcript that moved (or a prior fallback
		// that recorded the md log's own size) must not be treated as a
		// byte offset into this path.
		offset := int64(0)
		if ok && savedPath == path {
			offset = savedOffset
		}
		traj, newOffset, err := ReadFrom(path, offset)
		if err != nil {
			return IngestResult{}, err
		}
		rows := actionRows(traj, project, sessionName, projectPath, rid, cliSessionID, taskPtr)
		if len(rows) > 0 {
			if err := st.InsertActions(rows); err != nil {
				return IngestResult{}, err
			}
		}
		if err := st.SetIngestOffset(cliSessionID, path, newOffset); err != nil {
			return IngestResult{}, err
		}
		return IngestResult{Rows: len(rows)}, nil
	}

	if mdLogPath == "" {
		return IngestResult{}, findErr
	}
	if ok && savedPath == mdLogPath {
		return IngestResult{Reason: ReasonAlreadyIngestedMD}, nil
	}

	mdTraj, err := ParseLogFile(mdLogPath)
	if err != nil {
		// Report the original transcript-lookup failure — that's the error a
		// caller can actually act on; the fallback itself was best-effort.
		return IngestResult{}, findErr
	}
	rows := actionRows(mdTraj, project, sessionName, projectPath, rid, cliSessionID, taskPtr)
	if len(rows) > 0 {
		if err := st.InsertActions(rows); err != nil {
			return IngestResult{}, err
		}
	}
	var size int64
	if fi, statErr := os.Stat(mdLogPath); statErr == nil {
		size = fi.Size()
	}
	if err := st.SetIngestOffset(cliSessionID, mdLogPath, size); err != nil {
		return IngestResult{}, err
	}
	return IngestResult{Rows: len(rows), Reason: ReasonFallbackMD}, nil
}
