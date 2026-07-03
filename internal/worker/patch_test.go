package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ParsePatches ---

func TestParseSinglePatch(t *testing.T) {
	out := `### PATCH 1
FILE internal/foo.go
<<<FIND
func Foo() error {
	return nil
}
===REPLACE
func Foo() error {
	return errors.New("boom")
}
>>>END
`
	res, err := ParsePatches(out)
	if err != nil {
		t.Fatalf("ParsePatches: %v", err)
	}
	if len(res.Patches) != 1 || len(res.Warnings) != 0 {
		t.Fatalf("patches=%d warnings=%v, want 1 patch, no warnings", len(res.Patches), res.Warnings)
	}
	p := res.Patches[0]
	if p.Index != 1 || p.File != "internal/foo.go" {
		t.Errorf("header parsed wrong: %+v", p)
	}
	if !strings.Contains(p.Find, "return nil") || !strings.Contains(p.Replace, "boom") {
		t.Errorf("bodies wrong: FIND=%q REPLACE=%q", p.Find, p.Replace)
	}
}

func TestParseMultiplePatchesWithProseAndFences(t *testing.T) {
	out := "Here are the patches:\n```\n### PATCH 1\nFILE a.txt\n<<<FIND\nold a\n===REPLACE\nnew a\n>>>END\n```\nAnd the second one:\n### PATCH 2\nFILE b.txt\n<<<FIND\nold b\n===REPLACE\nnew b\n>>>END\ndone!\n"
	res, err := ParsePatches(out)
	if err != nil {
		t.Fatalf("ParsePatches: %v", err)
	}
	if len(res.Patches) != 2 {
		t.Fatalf("patches=%d, want 2", len(res.Patches))
	}
	if res.Patches[0].File != "a.txt" || res.Patches[1].File != "b.txt" {
		t.Errorf("files: %q, %q", res.Patches[0].File, res.Patches[1].File)
	}
	if res.Patches[1].Index != 2 {
		t.Errorf("index of second patch = %d, want 2", res.Patches[1].Index)
	}
}

// Golden: nemotron-ultra omits >>>END before the next patch header.
func TestParseMissingEndBeforeNextPatch(t *testing.T) {
	out := `### PATCH 1
FILE a.txt
<<<FIND
old a
===REPLACE
new a
### PATCH 2
FILE b.txt
<<<FIND
old b
===REPLACE
new b
>>>END
`
	res, err := ParsePatches(out)
	if err != nil {
		t.Fatalf("ParsePatches: %v", err)
	}
	if len(res.Patches) != 2 {
		t.Fatalf("patches=%d, want 2 (spliced)", len(res.Patches))
	}
	if res.Patches[0].Replace != "new a" {
		t.Errorf("spliced REPLACE=%q, want %q", res.Patches[0].Replace, "new a")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "missing >>>END") {
		t.Errorf("warnings=%v, want one missing->>>END warning", res.Warnings)
	}
}

// Golden: ===END emitted instead of >>>END.
func TestParseEndAltMarker(t *testing.T) {
	out := "### PATCH 1\nFILE a.txt\n<<<FIND\nold\n===REPLACE\nnew\n===END\n"
	res, err := ParsePatches(out)
	if err != nil {
		t.Fatalf("ParsePatches: %v", err)
	}
	if len(res.Patches) != 1 {
		t.Fatalf("patches=%d, want 1", len(res.Patches))
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "===END") {
		t.Errorf("warnings=%v, want ===END warning", res.Warnings)
	}
}

func TestParseEOFInsideReplaceSplices(t *testing.T) {
	out := "### PATCH 1\nFILE a.txt\n<<<FIND\nold\n===REPLACE\nnew"
	res, err := ParsePatches(out)
	if err != nil {
		t.Fatalf("ParsePatches: %v", err)
	}
	if len(res.Patches) != 1 || res.Patches[0].Replace != "new" {
		t.Fatalf("patches=%+v, want one with REPLACE=new", res.Patches)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "end of output") {
		t.Errorf("warnings=%v, want EOF splice warning", res.Warnings)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"eof inside FIND":       "### PATCH 1\nFILE a.txt\n<<<FIND\nold",
		"header inside FIND":    "### PATCH 1\nFILE a.txt\n<<<FIND\nold\n### PATCH 2\nFILE b.txt\n<<<FIND\nx\n===REPLACE\ny\n>>>END\n",
		"missing FILE":          "### PATCH 1\n<<<FIND\nold\n===REPLACE\nnew\n>>>END\n",
		"empty FILE path":       "### PATCH 1\nFILE\n<<<FIND\nold\n===REPLACE\nnew\n>>>END\n",
		"empty FIND":            "### PATCH 1\nFILE a.txt\n<<<FIND\n===REPLACE\nnew\n>>>END\n",
		"END before REPLACE":    "### PATCH 1\nFILE a.txt\n<<<FIND\nold\n>>>END\n",
		"no patches at all":     "I could not produce patches, sorry.\n",
		"garbage after FILE":    "### PATCH 1\nFILE a.txt\nsome commentary\n<<<FIND\nold\n===REPLACE\nnew\n>>>END\n",
		"eof right after PATCH": "### PATCH 1\nFILE a.txt\n",
	}
	for name, out := range cases {
		if _, err := ParsePatches(out); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestParseCRLFInput(t *testing.T) {
	out := strings.ReplaceAll("### PATCH 1\nFILE a.txt\n<<<FIND\nold\n===REPLACE\nnew\n>>>END\n", "\n", "\r\n")
	res, err := ParsePatches(out)
	if err != nil {
		t.Fatalf("ParsePatches: %v", err)
	}
	if res.Patches[0].Find != "old" || res.Patches[0].Replace != "new" {
		t.Errorf("CRLF not normalized: %+v", res.Patches[0])
	}
}

func TestHeaderIndexFallback(t *testing.T) {
	out := "### PATCH\nFILE a.txt\n<<<FIND\nold\n===REPLACE\nnew\n>>>END\n"
	res, err := ParsePatches(out)
	if err != nil {
		t.Fatalf("ParsePatches: %v", err)
	}
	if res.Patches[0].Index != 1 {
		t.Errorf("index=%d, want sequential fallback 1", res.Patches[0].Index)
	}
}

// --- ValidatePatch ---

func TestValidatePatchUniqueMatch(t *testing.T) {
	content := "a\nb\nc\n"
	if err := ValidatePatch(content, Patch{Index: 1, File: "f", Find: "b"}); err != nil {
		t.Errorf("unique match: %v", err)
	}
}

func TestValidatePatchMultipleMatches(t *testing.T) {
	content := "x\ny\nx\n"
	err := ValidatePatch(content, Patch{Index: 1, File: "f", Find: "x"})
	if err == nil || !strings.Contains(err.Error(), "2 locations") {
		t.Errorf("want 2-locations error, got: %v", err)
	}
}

// Golden: step37 drops a line from the middle of the anchor. The diagnostic
// must show the exact expected/actual pair at the divergence.
func TestValidatePatchDroppedLineDiagnostic(t *testing.T) {
	content := "func A() {\n\tx := 1\n\ty := 2\n\treturn x + y\n}\n"
	find := "func A() {\n\tx := 1\n\treturn x + y" // dropped "\ty := 2"
	err := ValidatePatch(content, Patch{Index: 3, File: "m.go", Find: find})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	msg := err.Error()
	for _, want := range []string{
		"PATCH 3 (m.go)",
		"first 2 line(s) match",
		`expected (FIND line 3): "\treturn x + y"`,
		`actual   (file line 3): "\ty := 2"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q:\n%s", want, msg)
		}
	}
}

func TestValidatePatchWhitespaceHint(t *testing.T) {
	content := "if ok {\n    doIt()\n}\n"
	find := "if ok {\n\tdoIt()" // tabs vs spaces on line 2
	err := ValidatePatch(content, Patch{Index: 1, File: "f", Find: find})
	if err == nil || !strings.Contains(err.Error(), "whitespace-only mismatch") {
		t.Errorf("want whitespace hint, got: %v", err)
	}
}

func TestValidatePatchFirstLineNowhere(t *testing.T) {
	err := ValidatePatch("a\nb\n", Patch{Index: 1, File: "f", Find: "zzz\nb"})
	if err == nil || !strings.Contains(err.Error(), "not found anywhere") {
		t.Errorf("want not-found-anywhere, got: %v", err)
	}
}

func TestValidatePatchFindPastEOF(t *testing.T) {
	err := ValidatePatch("a\nb", Patch{Index: 1, File: "f", Find: "a\nb\nc\nd"})
	if err == nil || !strings.Contains(err.Error(), "file ends after") {
		t.Errorf("want file-ends-after, got: %v", err)
	}
}

// --- CheckASCIIAnchors ---

func TestCheckASCIIAnchors(t *testing.T) {
	patches := []Patch{
		{Index: 1, File: "a.go", Find: "clean ascii", Replace: "нестрашно в REPLACE"},
		{Index: 2, File: "b.go", Find: "line1\n// комментарий\nline3"},
	}
	warnings := CheckASCIIAnchors(patches)
	if len(warnings) != 1 {
		t.Fatalf("warnings=%v, want exactly 1 (only FIND is checked)", warnings)
	}
	if !strings.Contains(warnings[0], "PATCH 2") || !strings.Contains(warnings[0], "FIND line 2") {
		t.Errorf("warning should name PATCH 2 line 2: %s", warnings[0])
	}
}

// --- ApplyPatches ---

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyPatchesSimple(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	res, err := ApplyPatches(dir, []Patch{{Index: 1, File: "a.txt", Find: "two", Replace: "TWO"}})
	if err != nil {
		t.Fatalf("ApplyPatches: %v", err)
	}
	if len(res.Applied) != 1 || len(res.Rejected) != 0 {
		t.Fatalf("applied=%d rejected=%v", len(res.Applied), res.Rejected)
	}
	if got := readFile(t, path); got != "one\nTWO\nthree\n" {
		t.Errorf("content=%q", got)
	}
}

func TestApplyPatchesSequentialSameFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "start\n")
	patches := []Patch{
		{Index: 1, File: "a.txt", Find: "start", Replace: "start\nmiddle"},
		{Index: 2, File: "a.txt", Find: "middle", Replace: "middle\nend"},
	}
	res, err := ApplyPatches(dir, patches)
	if err != nil {
		t.Fatalf("ApplyPatches: %v", err)
	}
	if len(res.Applied) != 2 {
		t.Fatalf("applied=%d rejected=%v, want 2 applied", len(res.Applied), res.Rejected)
	}
	if got := readFile(t, path); got != "start\nmiddle\nend\n" {
		t.Errorf("content=%q, second patch must see first patch's edit", got)
	}
}

func TestApplyPatchesRejectionDoesNotStopOthers(t *testing.T) {
	dir := t.TempDir()
	pathA := writeFile(t, dir, "a.txt", "hello\n")
	writeFile(t, dir, "b.txt", "same\nsame\n")
	patches := []Patch{
		{Index: 1, File: "missing.txt", Find: "x", Replace: "y"},
		{Index: 2, File: "b.txt", Find: "same", Replace: "diff"}, // ambiguous
		{Index: 3, File: "a.txt", Find: "hello", Replace: "bye"},
	}
	res, err := ApplyPatches(dir, patches)
	if err != nil {
		t.Fatalf("ApplyPatches: %v", err)
	}
	if len(res.Applied) != 1 || res.Applied[0].Index != 3 {
		t.Errorf("applied=%+v, want only patch 3", res.Applied)
	}
	if len(res.Rejected) != 2 {
		t.Fatalf("rejected=%+v, want 2", res.Rejected)
	}
	if !strings.Contains(res.Rejected[0].Reason, "cannot read file") {
		t.Errorf("reject 1 reason: %s", res.Rejected[0].Reason)
	}
	if !strings.Contains(res.Rejected[1].Reason, "2 locations") {
		t.Errorf("reject 2 reason: %s", res.Rejected[1].Reason)
	}
	if got := readFile(t, pathA); got != "bye\n" {
		t.Errorf("a.txt=%q", got)
	}
	// b.txt only touched by a rejected patch — must be untouched
	if got := readFile(t, filepath.Join(dir, "b.txt")); got != "same\nsame\n" {
		t.Errorf("b.txt modified despite rejection: %q", got)
	}
}

func TestApplyPatchesPathEscapes(t *testing.T) {
	dir := t.TempDir()
	outside := writeFile(t, t.TempDir(), "victim.txt", "data\n")
	patches := []Patch{
		{Index: 1, File: "../victim.txt", Find: "data", Replace: "owned"},
		{Index: 2, File: outside, Find: "data", Replace: "owned"},
		{Index: 3, File: "C:evil.txt", Find: "data", Replace: "owned"},
	}
	res, err := ApplyPatches(dir, patches)
	if err != nil {
		t.Fatalf("ApplyPatches: %v", err)
	}
	if len(res.Applied) != 0 || len(res.Rejected) != 3 {
		t.Fatalf("applied=%d rejected=%d, want 0/3", len(res.Applied), len(res.Rejected))
	}
	for _, r := range res.Rejected {
		if !strings.Contains(r.Reason, "not allowed") && !strings.Contains(r.Reason, "escapes worktree") {
			t.Errorf("PATCH %d: unexpected reason: %s", r.Patch.Index, r.Reason)
		}
	}
	if got := readFile(t, outside); got != "data\n" {
		t.Errorf("file outside worktree was modified: %q", got)
	}
}

func TestApplyPatchesNestedPathInsideRoot(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, filepath.Join("internal", "x", "f.go"), "old\n")
	res, err := ApplyPatches(dir, []Patch{{Index: 1, File: "internal/x/f.go", Find: "old", Replace: "new"}})
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("err=%v applied=%d", err, len(res.Applied))
	}
	if got := readFile(t, path); got != "new\n" {
		t.Errorf("content=%q", got)
	}
}

func TestApplyPatchesCRLFFilePreserved(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "one\r\ntwo\r\nthree\r\n")
	// Parser output is always LF; the file on disk is CRLF.
	res, err := ApplyPatches(dir, []Patch{{Index: 1, File: "a.txt", Find: "one\ntwo", Replace: "ONE\nTWO"}})
	if err != nil {
		t.Fatalf("ApplyPatches: %v", err)
	}
	if len(res.Applied) != 1 {
		t.Fatalf("rejected=%+v, want applied", res.Rejected)
	}
	if got := readFile(t, path); got != "ONE\r\nTWO\r\nthree\r\n" {
		t.Errorf("CRLF not preserved: %q", got)
	}
}

func TestApplyPatchesBadRoot(t *testing.T) {
	if _, err := ApplyPatches(filepath.Join(t.TempDir(), "nope"), nil); err == nil {
		t.Error("missing root: want error")
	}
	dir := t.TempDir()
	f := writeFile(t, dir, "f.txt", "x")
	if _, err := ApplyPatches(f, nil); err == nil {
		t.Error("root is a file: want error")
	}
}

func TestApplyPatchesNoTmpLeftovers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "old\n")
	if _, err := ApplyPatches(dir, []Patch{{Index: 1, File: "a.txt", Find: "old", Replace: "new"}}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("tmp file left behind: %s", e.Name())
		}
	}
}
