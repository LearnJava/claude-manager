package experience

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"claude-manager/internal/store"
)

func TestBackfillErrorText(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_TRANSCRIPTS_DIR", root)
	data, err := os.ReadFile("../../testdata/transcripts/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cli-1.jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	traj, err := Read(filepath.Join(root, "cli-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	errIdx := -1
	for _, s := range traj.Steps {
		if s.ResultIsError {
			errIdx = s.Index
		}
	}
	if errIdx < 0 {
		t.Fatal("fixture has no error step")
	}

	st := newTestStore(t)
	mk := func(cli string, idx int) store.ActionRow {
		return store.ActionRow{Project: "p", Session: "s", CLISessionID: cli, StepIndex: idx,
			Tool: "Write", Sig: "Write", IsError: true, Timestamp: time.Now()}
	}
	if err := st.InsertActions([]store.ActionRow{mk("cli-1", errIdx), mk("gone", 0), mk("", 0)}); err != nil {
		t.Fatal(err)
	}

	filled, missing, err := BackfillErrorText(st, map[string]string{"p": "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if filled != 1 || missing != 2 {
		t.Errorf("filled=%d missing=%d, want 1 and 2", filled, missing)
	}
	left, _ := st.ErrorRowsMissingText(10)
	if len(left) != 0 {
		t.Errorf("rows still selected: %d", len(left))
	}
	filled, missing, err = BackfillErrorText(st, nil)
	if err != nil || filled != 0 || missing != 0 {
		t.Errorf("second run filled=%d missing=%d err=%v", filled, missing, err)
	}
}
