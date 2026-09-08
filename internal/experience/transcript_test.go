package experience

import (
	"os"
	"path/filepath"
	"testing"
)

const fixture = "../../testdata/transcripts/sample.jsonl"

func TestReadFrom_ParsesFixture(t *testing.T) {
	traj, offset, err := ReadFrom(fixture, 0)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if offset <= 0 {
		t.Fatalf("expected positive offset, got %d", offset)
	}
	if traj.SessionID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("SessionID = %q", traj.SessionID)
	}
	if traj.ProjectPath != `D:\Project\example-app` {
		t.Errorf("ProjectPath = %q", traj.ProjectPath)
	}
	if traj.Branch != "main" {
		t.Errorf("Branch = %q", traj.Branch)
	}
	if traj.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (one malformed line)", traj.Skipped)
	}

	// Expected steps: text, tool_use(Bash), tool_use(Read), tool_use(Grep), text.
	// The empty thinking block, the plain-string user turn, and every
	// ignored/unknown line type must not produce a step.
	wantKinds := []StepKind{StepText, StepToolUse, StepToolUse, StepToolUse, StepText}
	if len(traj.Steps) != len(wantKinds) {
		t.Fatalf("got %d steps, want %d: %+v", len(traj.Steps), len(wantKinds), traj.Steps)
	}
	for i, k := range wantKinds {
		if traj.Steps[i].Kind != k {
			t.Errorf("step %d kind = %q, want %q", i, traj.Steps[i].Kind, k)
		}
		if traj.Steps[i].Index != i {
			t.Errorf("step %d Index = %d, want %d", i, traj.Steps[i].Index, i)
		}
	}

	bash := traj.Steps[1]
	if bash.ToolName != "Bash" {
		t.Errorf("step 1 ToolName = %q", bash.ToolName)
	}
	if bash.InputText != "git status --short" {
		t.Errorf("step 1 InputText = %q", bash.InputText)
	}
	if bash.ResultText != " M internal/app.go" {
		t.Errorf("step 1 ResultText = %q", bash.ResultText)
	}
	if bash.ResultChars != len(" M internal/app.go") {
		t.Errorf("step 1 ResultChars = %d", bash.ResultChars)
	}
	if bash.ResultIsError {
		t.Errorf("step 1 ResultIsError = true, want false")
	}
	if bash.Stdout != " M internal/app.go" {
		t.Errorf("step 1 Stdout = %q", bash.Stdout)
	}
	if bash.Usage == nil || bash.Usage.InputTokens != 130 {
		t.Errorf("step 1 Usage = %+v", bash.Usage)
	}

	readStep := traj.Steps[2]
	if readStep.ToolName != "Read" {
		t.Errorf("step 2 ToolName = %q", readStep.ToolName)
	}
	if readStep.InputText != "internal/app.go" {
		t.Errorf("step 2 InputText = %q", readStep.InputText)
	}
	if !readStep.ResultIsError {
		t.Errorf("step 2 ResultIsError = false, want true")
	}

	grep := traj.Steps[3]
	if grep.ToolName != "Grep" {
		t.Errorf("step 3 ToolName = %q", grep.ToolName)
	}
	// tool_result content was an array of {type,text} blocks — must be
	// flattened to plain text, exercising the second toolResultText shape.
	if grep.ResultText != "internal/app.go:12:func Run() error {" {
		t.Errorf("step 3 ResultText = %q", grep.ResultText)
	}

	if traj.Steps[0].InputText != "I'll check the current git state first." {
		t.Errorf("step 0 InputText = %q", traj.Steps[0].InputText)
	}
	if traj.Steps[4].InputText != "Found it — build error is a missing return. Fixed." {
		t.Errorf("step 4 InputText = %q", traj.Steps[4].InputText)
	}
}

func TestReadFrom_IncrementalOffsetNoDuplicates(t *testing.T) {
	full, _, err := ReadFrom(fixture, 0)
	if err != nil {
		t.Fatalf("ReadFrom full: %v", err)
	}

	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	// Split roughly in half, but only at a line boundary, so the first read
	// sees a legitimate prefix of complete lines.
	mid := len(data) / 2
	nl := mid
	for nl < len(data) && data[nl] != '\n' {
		nl++
	}
	splitAt := int64(nl + 1)

	// Simulate two ingest passes against a growing file by reading the two
	// halves of the same file directly.
	tmp := filepath.Join(t.TempDir(), "resume.jsonl")
	if err := os.WriteFile(tmp, data[:splitAt], 0o644); err != nil {
		t.Fatal(err)
	}
	traj1, offset1, err := ReadFrom(tmp, 0)
	if err != nil {
		t.Fatalf("ReadFrom pass 1: %v", err)
	}
	if offset1 != splitAt {
		t.Fatalf("offset1 = %d, want %d (all complete lines consumed)", offset1, splitAt)
	}

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		t.Fatal(err)
	}
	traj2, offset2, err := ReadFrom(tmp, offset1)
	if err != nil {
		t.Fatalf("ReadFrom pass 2: %v", err)
	}
	if offset2 != int64(len(data)) {
		t.Fatalf("offset2 = %d, want %d", offset2, len(data))
	}

	totalSteps := len(traj1.Steps) + len(traj2.Steps)
	if totalSteps != len(full.Steps) {
		t.Errorf("resumed read produced %d steps total, want %d (no dup/loss)", totalSteps, len(full.Steps))
	}
}

func TestReadFrom_IncompleteLastLineNotConsumed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.jsonl")

	line1 := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]},"sessionId":"s1","timestamp":"2026-09-08T10:00:00.000Z"}` + "\n"
	partial := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text",`

	if err := os.WriteFile(path, []byte(line1+partial), 0o644); err != nil {
		t.Fatal(err)
	}

	traj, offset, err := ReadFrom(path, 0)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(traj.Steps) != 1 {
		t.Fatalf("got %d steps, want 1 (partial line must not be parsed)", len(traj.Steps))
	}
	if offset != int64(len(line1)) {
		t.Fatalf("offset = %d, want %d (must not advance past the incomplete line)", offset, len(line1))
	}
	if traj.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0 (an unconsumed partial line is not a parse error)", traj.Skipped)
	}

	// Complete the line and re-read from the saved offset: now it should
	// appear, and only once.
	rest := `"text":"world"}]},"sessionId":"s1","timestamp":"2026-09-08T10:00:01.000Z"}` + "\n"
	if err := os.WriteFile(path, []byte(line1+partial+rest), 0o644); err != nil {
		t.Fatal(err)
	}
	traj2, offset2, err := ReadFrom(path, offset)
	if err != nil {
		t.Fatalf("ReadFrom resumed: %v", err)
	}
	if len(traj2.Steps) != 1 {
		t.Fatalf("got %d steps on resume, want 1", len(traj2.Steps))
	}
	if traj2.Steps[0].InputText != "world" {
		t.Errorf("resumed step InputText = %q", traj2.Steps[0].InputText)
	}
	if offset2 != int64(len(line1)+len(partial)+len(rest)) {
		t.Errorf("offset2 = %d, want end of file", offset2)
	}
}

func TestReadFrom_MalformedLineSkippedNotFatal(t *testing.T) {
	traj, _, err := ReadFrom(fixture, 0)
	if err != nil {
		t.Fatalf("ReadFrom must not fail on a malformed line: %v", err)
	}
	if traj.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", traj.Skipped)
	}
}

func TestReadFrom_MissingFile(t *testing.T) {
	if _, _, err := ReadFrom(filepath.Join(t.TempDir(), "nope.jsonl"), 0); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func TestProjectSlug(t *testing.T) {
	cases := map[string]string{
		`D:\GolangProjects\claude-manager`: "D--GolangProjects-claude-manager",
		`/home/user/my-project`:            "-home-user-my-project",
	}
	for in, want := range cases {
		if got := ProjectSlug(in); got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindTranscript_DirectSlugMatch(t *testing.T) {
	root := t.TempDir()
	projectPath := `D:\Some\Project`
	slugDir := filepath.Join(root, ProjectSlug(projectPath))
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(slugDir, "abc-123.jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := FindTranscript(root, projectPath, "abc-123")
	if err != nil {
		t.Fatalf("FindTranscript: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindTranscript_FallsBackToScan(t *testing.T) {
	root := t.TempDir()
	// Session lives under a differently-named directory than the slug of
	// projectPath would predict (e.g. the project folder moved).
	otherDir := filepath.Join(root, "some-other-slug")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(otherDir, "xyz-999.jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := FindTranscript(root, `D:\Nonexistent\Path`, "xyz-999")
	if err != nil {
		t.Fatalf("FindTranscript: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindTranscript_NotFound(t *testing.T) {
	root := t.TempDir()
	if _, err := FindTranscript(root, `D:\X`, "missing"); err == nil {
		t.Error("expected an error when the transcript doesn't exist anywhere under root")
	}
}

func TestTranscriptsRoot_HonorsEnvOverride(t *testing.T) {
	t.Setenv("CM_TRANSCRIPTS_DIR", `D:\custom\transcripts`)
	if got := TranscriptsRoot(); got != `D:\custom\transcripts` {
		t.Errorf("TranscriptsRoot() = %q", got)
	}
}
