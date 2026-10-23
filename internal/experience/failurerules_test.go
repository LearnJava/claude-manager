package experience

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/store"
)

func errRow(run int64, step int, tool, sig, arg, errText string, isErr bool, ts time.Time) store.ActionRow {
	cli := fmt.Sprintf("r%d", run)
	return store.ActionRow{Project: "p", Session: "S", CLISessionID: cli, StepIndex: step, Tool: tool,
		Sig: sig, Arg: arg, IsError: isErr, ErrText: errText, Timestamp: ts}
}

func TestErrorClustersMergeAcrossCommands(t *testing.T) {
	now := time.Now().UTC()
	rows := []store.ActionRow{
		errRow(1, 0, "Bash", "Bash:a", "cmd one", "fatal: bad ref 12", true, now),
		errRow(1, 1, "Bash", "Bash:a", "cmd one -v", "", false, now),
		errRow(2, 0, "Bash", "Bash:b", "cmd two", "fatal: bad ref 99", true, now),
		errRow(2, 1, "Bash", "Bash:c", "cmd three", "", false, now),
	}
	got := ErrorClusters(rows)
	if len(got) != 1 || got[0].DistinctRuns != 2 || got[0].Count != 2 {
		t.Fatalf("clusters = %+v", got)
	}
	ex := got[0].Examples
	if len(ex) != 2 || ex[0].FixedArg != "cmd one -v" { // same Sig
		t.Errorf("examples = %+v", ex)
	}
	if ex[1].FixedArg != "cmd three" { // same Tool, different Sig
		t.Errorf("tool fallback = %+v", ex[1])
	}
}

func TestErrorClustersSkipNoise(t *testing.T) {
	now := time.Now().UTC()
	var rows []store.ActionRow
	for i, msg := range []string{"The user doesn't want to proceed with this tool use", "[Tool call interrupted by user]",
		"Permission to use Bash has been denied", "read ECONNRESET", "Request interrupted by user"} {
		rows = append(rows, errRow(int64(i+1), 0, "Bash", "Bash:x", "x", msg, true, now))
	}
	if got := ErrorClusters(rows); len(got) != 0 {
		t.Errorf("noise clustered: %+v", got)
	}
}

func TestAppendClaudeMdRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	read := func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	if err := AppendClaudeMdRule(dir, "rule A"); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "## Learned from failures\n\n- rule A\n" {
		t.Errorf("created = %q", got)
	}
	if err := AppendClaudeMdRule(dir, "rule A"); err != nil {
		t.Fatal(err)
	}
	if strings.Count(read(), "rule A") != 1 {
		t.Errorf("not idempotent: %q", read())
	}

	os.WriteFile(path, []byte("# Proj\n\ntext\n"), 0o644)
	AppendClaudeMdRule(dir, "rule B")
	if got := read(); got != "# Proj\n\ntext\n\n## Learned from failures\n\n- rule B\n" {
		t.Errorf("section appended = %q", got)
	}

	os.WriteFile(path, []byte("## Learned from failures\n\n- old\n\n## Other\n\nx\n"), 0o644)
	AppendClaudeMdRule(dir, "rule C")
	if got := read(); got != "## Learned from failures\n\n- old\n- rule C\n\n## Other\n\nx\n" {
		t.Errorf("before next heading = %q", got)
	}

	os.WriteFile(path, []byte("# P\r\n\r\n## Learned from failures\r\n\r\n- old\r\n"), 0o644)
	AppendClaudeMdRule(dir, "rule D")
	if got := read(); got != "# P\r\n\r\n## Learned from failures\r\n\r\n- old\r\n- rule D\r\n" {
		t.Errorf("crlf = %q", got)
	}
}

func TestFailureRuleEffect(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	decided := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	var rows []store.ActionRow
	for i := 0; i < 20; i++ { // 20 runs before, 3 hit
		ts := decided.Add(-time.Duration(i+1) * time.Hour)
		rows = append(rows, errRow(int64(i+1), 0, "Bash", "Bash:x", "c", "", i < 3, ts))
		if i < 3 {
			rows[len(rows)-1].ErrText = "fatal: boom 5"
		}
	}
	rows = append(rows, errRow(100, 0, "Bash", "Bash:x", "old", "fatal: boom", true, decided.Add(-100*time.Hour))) // 21st, dropped
	for i := 0; i < 5; i++ { // 5 runs after, 0 hit
		rows = append(rows, errRow(int64(200+i), 0, "Bash", "Bash:x", "c", "other", true, decided.Add(time.Duration(i+1)*time.Minute)))
	}
	if err := st.InsertActions(rows); err != nil {
		t.Fatal(err)
	}
	rule := store.FailureRule{ErrorKey: ErrorKey("fatal: boom 5"), Status: store.FailureRuleAccepted, DecidedAt: &decided}
	eff, err := FailureRuleEffect(st, "p", rule)
	if err != nil {
		t.Fatal(err)
	}
	if eff != (FailureEffect{RunsBefore: 20, HitsBefore: 3, RunsAfter: 5, HitsAfter: 0}) {
		t.Errorf("effect = %+v", eff)
	}
	rule.Status = store.FailureRuleDraft
	if eff, _ := FailureRuleEffect(st, "p", rule); eff != (FailureEffect{}) {
		t.Errorf("draft effect = %+v", eff)
	}
}

func TestFailureRuleCandidates(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	var rows []store.ActionRow
	for i := 1; i <= 3; i++ {
		rows = append(rows, errRow(int64(i), 0, "Bash", "Bash:x", "c", "fatal: boom", true, now))
		rows = append(rows, errRow(int64(i), 1, "Bash", "Bash:y", "d", "no such thing", true, now))
	}
	st.InsertActions(rows)
	got, err := FailureRuleCandidates(st, "p")
	if err != nil || len(got) != 2 {
		t.Fatalf("candidates = %+v, %v", got, err)
	}
	st.InsertFailureRule(&store.FailureRule{Project: "p", ErrorKey: ErrorKey("fatal: boom"), Status: store.FailureRuleDismissed})
	got, _ = FailureRuleCandidates(st, "p")
	if len(got) != 1 || got[0].ErrorKey != "no such thing" {
		t.Errorf("after rule = %+v", got)
	}
}
