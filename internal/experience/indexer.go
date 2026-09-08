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
func IngestDir(st *store.Store, root, project string, opts ImportOpts) (ImportStats, error) {
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
		ingestOne(st, path, project, &stats)
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
func ingestOne(st *store.Store, path, project string, stats *ImportStats) {
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

	rows := actionRows(traj, project, traj.SessionID, nil, name, "")
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
func actionRows(traj Trajectory, project, sessionName string, runID *int64, cliSessionID, taskPtr string) []store.ActionRow {
	var rows []store.ActionRow
	for _, step := range traj.Steps {
		if step.Kind != StepToolUse || step.ToolName == "" {
			continue
		}
		sig, arg := Signature(step.ToolName, step.InputText, traj.ProjectPath)
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

// IngestRun indexes the newly-appended portion of one finished run's CLI
// JSONL transcript into action_signatures (LEARN-TASKS.md LN-03), called
// from SessionManager.finishRun when experience_tracking is on. It is
// idempotent via the per-CLI-session byte offset in ingest_state
// (GetIngestOffset/SetIngestOffset, LN-02): re-running it for the same
// cliSessionID only reads what wasn't read last time, so a retried or
// duplicate call never re-inserts rows.
//
// A live run's transcript (unlike a closed, bulk-imported log file) is the
// one place a real run_id is available, so rows carry it — TopSignatures'
// DistinctRuns then counts these rows by run_id directly, falling back to
// cli_session_id only for the bulk-imported rows that have none.
func IngestRun(st *store.Store, project, sessionName string, runID int64, cliSessionID, projectPath, taskPtr string) error {
	if cliSessionID == "" {
		// No system/init event ever arrived (e.g. the process died before
		// producing one) — there is no transcript file to find.
		return nil
	}

	path, err := FindTranscript(TranscriptsRoot(), projectPath, cliSessionID)
	if err != nil {
		return err
	}

	_, offset, ok, err := st.GetIngestOffset(cliSessionID)
	if err != nil {
		return err
	}
	if !ok {
		offset = 0
	}

	traj, newOffset, err := ReadFrom(path, offset)
	if err != nil {
		return err
	}

	var rid *int64
	if runID != 0 {
		rid = &runID
	}
	rows := actionRows(traj, project, sessionName, rid, cliSessionID, taskPtr)
	if len(rows) > 0 {
		if err := st.InsertActions(rows); err != nil {
			return err
		}
	}
	return st.SetIngestOffset(cliSessionID, path, newOffset)
}
