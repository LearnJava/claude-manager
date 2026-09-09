package experience

import (
	"encoding/json"
	"strings"
	"testing"

	"claude-manager/internal/store"
	"claude-manager/internal/worker"
)

// TestExtractFromMixedTasks_FailThenFix unmarshals a MixedTask JSON fixture
// (source A, LEARN-TASKS.md LN-07) whose round 1 fails a gate and round 2
// passes it, and checks the fix pair is built from the gate's own failed
// command/output and the very next round's applied patches — never from the
// model's own prose.
func TestExtractFromMixedTasks_FailThenFix(t *testing.T) {
	const fixtureJSON = `{
		"ID": "proj/brief/step37",
		"Project": "proj",
		"Rounds": [
			{
				"Number": 1,
				"Gates": {
					"Commands": [
						{"Command": "go test ./...", "Output": "--- FAIL: TestFoo\nfatal: 'origin' does not appear to be a git repository", "ExitCode": 1}
					],
					"Passed": false
				},
				"Passed": false
			},
			{
				"Number": 2,
				"Applied": [
					{"Index": 1, "File": "internal/foo.go", "Find": "a", "Replace": "b"},
					{"Index": 2, "File": "internal/foo.go", "Find": "c", "Replace": "d"},
					{"Index": 3, "File": "internal/bar.go", "Find": "e", "Replace": "f"}
				],
				"Gates": {"Passed": true},
				"Passed": true
			}
		]
	}`
	var task worker.MixedTask
	if err := json.Unmarshal([]byte(fixtureJSON), &task); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	pairs := ExtractFromMixedTasks([]*worker.MixedTask{&task})
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want 1: %+v", len(pairs), pairs)
	}
	p := pairs[0]
	if p.RunKey != "proj/brief/step37" {
		t.Errorf("RunKey = %q, want the task ID", p.RunKey)
	}
	if p.FailedArg != "go test ./..." {
		t.Errorf("FailedArg = %q, want %q", p.FailedArg, "go test ./...")
	}
	if !strings.Contains(p.Output, "fatal: 'origin' does not appear to be a git repository") {
		t.Errorf("Output = %q, want it to carry the gate's raw failure text", p.Output)
	}
	// File list deduplicated, in first-seen order.
	if p.FixedArg != "patched internal/foo.go, internal/bar.go" {
		t.Errorf("FixedArg = %q, want deduplicated file list", p.FixedArg)
	}
}

// TestExtractFromMixedTasks_NoPairWithoutBothHalves covers the two ways a
// round transition must NOT yield a pair: the "failure" round already passed
// (nothing to fix), and a failure that is followed by another failure
// (nothing fixed it yet — LN-09 must never learn from an unresolved gate).
func TestExtractFromMixedTasks_NoPairWithoutBothHalves(t *testing.T) {
	alreadyPassing := &worker.MixedTask{
		ID: "proj/b/w1",
		Rounds: []worker.RoundRecord{
			{Number: 1, Passed: true, Gates: worker.GateResult{Passed: true}},
			{Number: 2, Passed: false, Gates: worker.GateResult{
				Commands: []worker.GateCommandResult{{Command: "go vet ./...", Output: "vet: issue", ExitCode: 1}},
			}},
		},
	}
	stillFailing := &worker.MixedTask{
		ID: "proj/b/w2",
		Rounds: []worker.RoundRecord{
			{Number: 1, Passed: false, Gates: worker.GateResult{
				Commands: []worker.GateCommandResult{{Command: "go build ./...", Output: "build error", ExitCode: 1}},
			}},
			{Number: 2, Passed: false, Gates: worker.GateResult{
				Commands: []worker.GateCommandResult{{Command: "go build ./...", Output: "build error again", ExitCode: 1}},
			}},
		},
	}
	pairs := ExtractFromMixedTasks([]*worker.MixedTask{alreadyPassing, stillFailing})
	if len(pairs) != 0 {
		t.Errorf("got %d pairs, want 0: %+v", len(pairs), pairs)
	}
}

// TestExtractFromRun_WithinWindow builds synthetic action_signatures rows for
// one run (LEARN-TASKS.md LN-07, source B): a failing call followed a few
// steps later by a successful retry of the exact same signature must pair up
// with both verbatim arg strings kept, and only the earliest working retry
// counts.
func TestExtractFromRun_WithinWindow(t *testing.T) {
	rows := []store.ActionRow{
		{StepIndex: 0, Sig: "Bash:git remote", Arg: "git remote -v", IsError: true},
		{StepIndex: 1, Sig: "Bash:git status", Arg: "git status", IsError: false},
		{StepIndex: 2, Sig: "Bash:git remote", Arg: "git remote add origin <ARG>", IsError: false},
		{StepIndex: 3, Sig: "Bash:git remote", Arg: "git remote -v", IsError: false}, // later success, must not double-count
	}
	pairs := ExtractFromRun(rows, "run:1")
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want 1: %+v", len(pairs), pairs)
	}
	p := pairs[0]
	if p.RunKey != "run:1" || p.FailedArg != "git remote -v" || p.FixedArg != "git remote add origin <ARG>" {
		t.Errorf("pair = %+v, want {run:1, git remote -v, git remote add origin <ARG>}", p)
	}
}

// TestExtractFromRun_OutsideWindowNotPaired checks the FailurePairWindow
// boundary: a same-signature success more than 5 steps after the failure is
// not evidence of a fix — too much else happened in between.
func TestExtractFromRun_OutsideWindowNotPaired(t *testing.T) {
	rows := []store.ActionRow{
		{StepIndex: 0, Sig: "Bash:cargo build", Arg: "cargo build", IsError: true},
	}
	for i := 1; i <= 5; i++ {
		rows = append(rows, store.ActionRow{StepIndex: i, Sig: "Bash:ls", Arg: "ls", IsError: false})
	}
	rows = append(rows, store.ActionRow{StepIndex: 6, Sig: "Bash:cargo build", Arg: "cargo build --release", IsError: false})
	if pairs := ExtractFromRun(rows, "run:1"); len(pairs) != 0 {
		t.Errorf("got %d pairs, want 0 (success is 6 steps after the failure): %+v", len(pairs), pairs)
	}
}

// TestExtractFromActionRows_GroupsByRun checks the project-wide entry point
// keeps two runs isolated (a same-signature failure in one run must not pair
// with a success several steps away in a different run) and falls back to
// cli_session_id when RunID is nil (a bulk-imported row).
func TestExtractFromActionRows_GroupsByRun(t *testing.T) {
	runID := int64(42)
	rows := []store.ActionRow{
		// Run 1 (a live run, has RunID): fails then fixes.
		{RunID: &runID, StepIndex: 0, Sig: "Bash:git branch", Arg: "git branch -a", IsError: true},
		{RunID: &runID, StepIndex: 1, Sig: "Bash:git branch", Arg: "git branch --show-current", IsError: false},
		// Run 2 (bulk-imported, no RunID): its own failure, unrelated to run 1.
		{CLISessionID: "cli-x", StepIndex: 0, Sig: "Bash:npm test", Arg: "npm test", IsError: true},
		{CLISessionID: "cli-x", StepIndex: 1, Sig: "Bash:npm test", Arg: "npm test -- --ci", IsError: false},
	}
	pairs := ExtractFromActionRows(rows)
	if len(pairs) != 2 {
		t.Fatalf("got %d pairs, want 2: %+v", len(pairs), pairs)
	}
	seen := map[string]FixPair{}
	for _, p := range pairs {
		seen[p.RunKey] = p
	}
	if p, ok := seen["run:42"]; !ok || p.FixedArg != "git branch --show-current" {
		t.Errorf("run:42 pair = %+v, ok=%v", p, ok)
	}
	if p, ok := seen["cli:cli-x"]; !ok || p.FixedArg != "npm test -- --ci" {
		t.Errorf("cli:cli-x pair = %+v, ok=%v", p, ok)
	}
}

// TestErrorKey_CorpusClusters exercises ErrorKey on five real corpus error
// shapes (LEARN-TASKS.md LN-07's table), each with a path- or number-varying
// second occurrence: both must normalize to the same key ("две записи с
// разными путями → один кластер").
func TestErrorKey_CorpusClusters(t *testing.T) {
	cases := []struct {
		name        string
		a, b        string
		mustContain string
	}{
		{
			name:        "git remote not configured",
			a:           "fatal: 'origin' does not appear to be a git repository",
			b:           "fatal: 'origin' does not appear to be a git repository",
			mustContain: "fatal: 'origin' does not appear to be a git repository",
		},
		{
			name:        "main vs master",
			a:           "fatal: ambiguous argument 'main': unknown revision or path not in the working tree.",
			b:           "fatal: ambiguous argument 'main': unknown revision or path not in the working tree.",
			mustContain: "fatal: ambiguous argument 'main'",
		},
		{
			name:        "file too large — token counts differ",
			a:           "File content (52341 tokens) exceeds maximum allowed tokens (25000). Use offset and limit to read specific portions of the file.",
			b:           "File content (118422 tokens) exceeds maximum allowed tokens (25000). Use offset and limit to read specific portions of the file.",
			mustContain: "File content (N tokens) exceeds maximum allowed tokens (N)",
		},
		{
			name:        "read before write — paths differ (Windows vs POSIX)",
			a:           `Error editing D:\GolangProjects\claude-manager\internal\app.go: File has not been read yet. Read it first before writing to it.`,
			b:           "Error editing /home/dev/project/internal/app.go: File has not been read yet. Read it first before writing to it.",
			mustContain: "File has not been read yet.",
		},
		{
			name:        "locked worktree — paths differ",
			a:           `fatal: cannot remove a locked working tree, lock reason: claude session (path: D:\GolangProjects\claude-manager\.claude\worktrees\p1-work)`,
			b:           "fatal: cannot remove a locked working tree, lock reason: claude session (path: /home/dev/project/.claude/worktrees/p2-work)",
			mustContain: "cannot remove a locked working tree, lock reason: claude session",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ka, kb := ErrorKey(c.a), ErrorKey(c.b)
			if ka != kb {
				t.Errorf("ErrorKey(a) = %q, ErrorKey(b) = %q — must collapse to one cluster", ka, kb)
			}
			if !strings.Contains(ka, c.mustContain) {
				t.Errorf("ErrorKey = %q, want it to still contain %q", ka, c.mustContain)
			}
			if strings.ContainsAny(ka, `\`) || strings.Contains(ka, "GolangProjects") || strings.Contains(ka, "/home/dev") {
				t.Errorf("ErrorKey = %q, must not leak an absolute path", ka)
			}
		})
	}
}

// TestErrorKey_TruncatesLongLines checks the 80-rune cap (LEARN-TASKS.md
// LN-07) and that only the first line is considered.
func TestErrorKey_TruncatesLongLines(t *testing.T) {
	long := strings.Repeat("x", 200) + "\nsecond line is ignored"
	key := ErrorKey(long)
	if len([]rune(key)) != errorKeyMaxRunes {
		t.Errorf("len(ErrorKey) = %d, want %d", len([]rune(key)), errorKeyMaxRunes)
	}
	if strings.Contains(key, "second line") {
		t.Errorf("ErrorKey = %q, must not include text past the first line", key)
	}
}

// TestCluster_GroupsAndRanksByDistinctRuns checks Cluster merges pairs whose
// error text differs only by path/number into one cluster, counts
// DistinctRuns (not raw Count) per RunKey, and sorts the result with the
// widest-spread cluster first.
func TestCluster_GroupsAndRanksByDistinctRuns(t *testing.T) {
	pairs := []FixPair{
		// Same cluster, three distinct runs, one run repeats the pair twice.
		{RunKey: "run:1", Output: "fatal: 'origin' does not appear to be a git repository"},
		{RunKey: "run:1", Output: "fatal: 'origin' does not appear to be a git repository"},
		{RunKey: "run:2", Output: "fatal: 'origin' does not appear to be a git repository"},
		{RunKey: "run:3", Output: "fatal: 'origin' does not appear to be a git repository"},
		// A different, single-run cluster.
		{RunKey: "run:4", Output: "fatal: ambiguous argument 'main': unknown revision"},
	}
	clusters := Cluster(pairs)
	if len(clusters) != 2 {
		t.Fatalf("got %d clusters, want 2: %+v", len(clusters), clusters)
	}
	top := clusters[0]
	if top.DistinctRuns != 3 {
		t.Errorf("top cluster DistinctRuns = %d, want 3", top.DistinctRuns)
	}
	if top.Count != 4 {
		t.Errorf("top cluster Count = %d, want 4 (run:1's repeat counts twice)", top.Count)
	}
	if clusters[1].DistinctRuns != 1 {
		t.Errorf("second cluster DistinctRuns = %d, want 1", clusters[1].DistinctRuns)
	}
}

// TestCluster_FallsBackToFailedArgWhenNoOutput checks a source-B pair (no
// Output text) still clusters, keyed off its FailedArg.
func TestCluster_FallsBackToFailedArgWhenNoOutput(t *testing.T) {
	pairs := []FixPair{
		{RunKey: "run:1", FailedArg: "git remote -v", FixedArg: "git remote add origin <ARG>"},
		{RunKey: "run:2", FailedArg: "git remote -v", FixedArg: "git remote add origin <ARG>"},
	}
	clusters := Cluster(pairs)
	if len(clusters) != 1 {
		t.Fatalf("got %d clusters, want 1: %+v", len(clusters), clusters)
	}
	if clusters[0].DistinctRuns != 2 {
		t.Errorf("DistinctRuns = %d, want 2", clusters[0].DistinctRuns)
	}
	if clusters[0].ErrorKey != "git remote -v" {
		t.Errorf("ErrorKey = %q, want the failed arg verbatim (no output to normalize instead)", clusters[0].ErrorKey)
	}
}

// TestCluster_ExamplesCapped checks maxClusterExamples bounds the Examples
// slice even when a cluster has many more occurrences.
func TestCluster_ExamplesCapped(t *testing.T) {
	var pairs []FixPair
	for i := 0; i < maxClusterExamples+10; i++ {
		pairs = append(pairs, FixPair{RunKey: "run", Output: "fatal: 'origin' does not appear to be a git repository"})
	}
	clusters := Cluster(pairs)
	if len(clusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(clusters))
	}
	if len(clusters[0].Examples) != maxClusterExamples {
		t.Errorf("len(Examples) = %d, want %d", len(clusters[0].Examples), maxClusterExamples)
	}
	if clusters[0].Count != maxClusterExamples+10 {
		t.Errorf("Count = %d, want %d (capping Examples must not cap Count)", clusters[0].Count, maxClusterExamples+10)
	}
}
