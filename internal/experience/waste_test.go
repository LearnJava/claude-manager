package experience

import (
	"path/filepath"
	"testing"
	"time"

	"claude-manager/internal/store"
)

// wasteSeq builds one run's rows: each {tool, arg, chars} becomes the next
// step. A tool suffixed "!" is a failed call.
type wcall struct {
	tool  string
	arg   string
	chars int
	err   bool
}

func wasteRows(calls ...wcall) []store.ActionRow {
	runID := int64(7)
	rows := make([]store.ActionRow, len(calls))
	for i, c := range calls {
		rows[i] = store.ActionRow{
			ID: int64(i + 1), Project: "p", Session: "P1", RunID: &runID,
			StepIndex: i, Tool: c.tool, Arg: c.arg, ResultChars: c.chars, IsError: c.err,
		}
	}
	return rows
}

func findCat(t *testing.T, r WasteReport, cat string) WasteCategory {
	t.Helper()
	for _, c := range r.Categories {
		if c.Category == cat {
			return c
		}
	}
	t.Fatalf("category %s not in report %+v", cat, r.Categories)
	return WasteCategory{}
}

func hasCat(r WasteReport, cat string) bool {
	for _, c := range r.Categories {
		if c.Category == cat {
			return true
		}
	}
	return false
}

func TestRepeatReadSameFileSameResult(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "a.go", chars: 4000},
		wcall{tool: "Bash", arg: "ls", chars: 40},
		wcall{tool: "Read", arg: "a.go", chars: 4000},
	), WasteThresholds{})
	c := findCat(t, r, WasteRepeatRead)
	if c.Count != 1 || c.EstTokens != 1000 {
		t.Fatalf("got count=%d tokens=%d, want 1 and 1000", c.Count, c.EstTokens)
	}
	if len(c.Examples) != 1 || c.Examples[0].Step != 2 || c.Examples[0].Arg != "a.go" || c.Examples[0].RunID != 7 {
		t.Fatalf("example = %+v", c.Examples)
	}
}

func TestRepeatReadAfterEditOfSameFileIsNotWaste(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "/x/a.go", chars: 4000},
		wcall{tool: "Edit", arg: "/x/a.go", chars: 20},
		wcall{tool: "Read", arg: "/x/a.go", chars: 4000},
	), WasteThresholds{})
	if hasCat(r, WasteRepeatRead) {
		t.Fatalf("a read after an edit of the same file is not a repeat: %+v", r.Categories)
	}
}

func TestRepeatReadAfterEditOfOtherFileStillWaste(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "a.go", chars: 4000},
		wcall{tool: "Edit", arg: "b.go", chars: 20},
		wcall{tool: "Read", arg: "a.go", chars: 4000},
	), WasteThresholds{})
	if findCat(t, r, WasteRepeatRead).Count != 1 {
		t.Fatalf("an edit of another file must not excuse the re-read: %+v", r.Categories)
	}
}

func TestRepeatReadPathCaseAndSlashInsensitive(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: `D:\Proj\A.go`, chars: 4000},
		wcall{tool: "Write", arg: "d:/proj/a.go", chars: 20},
		wcall{tool: "Read", arg: `D:\Proj\A.go`, chars: 4000},
	), WasteThresholds{})
	if hasCat(r, WasteRepeatRead) {
		t.Fatalf("Windows path spelling must not hide an edit: %+v", r.Categories)
	}
}

func TestRepeatReadSlicesOfDifferentSizeAreNotRepeats(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "big.go", chars: 8000},
		wcall{tool: "Read", arg: "big.go", chars: 6000},
		wcall{tool: "Read", arg: "big.go", chars: 2000},
	), WasteThresholds{})
	if hasCat(r, WasteRepeatRead) {
		t.Fatalf("reading a file in slices is not a re-read: %+v", r.Categories)
	}
}

func TestRepeatSearchResetByAnyEdit(t *testing.T) {
	waste := AnalyzeWaste(wasteRows(
		wcall{tool: "Grep", arg: "TODO", chars: 900},
		wcall{tool: "Glob", arg: "*.go", chars: 300},
		wcall{tool: "Grep", arg: "TODO", chars: 900},
		wcall{tool: "Glob", arg: "*.go", chars: 300},
	), WasteThresholds{})
	if findCat(t, waste, WasteRepeatRead).Count != 2 {
		t.Fatalf("both repeated searches must count: %+v", waste.Categories)
	}
	clean := AnalyzeWaste(wasteRows(
		wcall{tool: "Grep", arg: "TODO", chars: 900},
		wcall{tool: "Edit", arg: "a.go", chars: 20},
		wcall{tool: "Grep", arg: "TODO", chars: 900},
	), WasteThresholds{})
	if hasCat(clean, WasteRepeatRead) {
		t.Fatalf("a search after an edit is not a repeat: %+v", clean.Categories)
	}
}

func TestRepeatReadIgnoresFailedCalls(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "a.go", chars: 400, err: true},
		wcall{tool: "Read", arg: "a.go", chars: 400},
	), WasteThresholds{})
	if hasCat(r, WasteRepeatRead) {
		t.Fatalf("a failed read does not count as the first read: %+v", r.Categories)
	}
}

func TestEmptySearchNeedsStreakOfDifferentArgs(t *testing.T) {
	streak := AnalyzeWaste(wasteRows(
		wcall{tool: "Grep", arg: "foo", chars: 17},
		wcall{tool: "Glob", arg: "**/bar*", chars: 15},
		wcall{tool: "Grep", arg: "baz", chars: 17},
	), WasteThresholds{})
	if c := findCat(t, streak, WasteEmptySearch); c.Count != 3 {
		t.Fatalf("a streak of 3 empty searches: count=%d", c.Count)
	}

	single := AnalyzeWaste(wasteRows(
		wcall{tool: "Grep", arg: "foo", chars: 17},
		wcall{tool: "Read", arg: "a.go", chars: 400},
		wcall{tool: "Grep", arg: "bar", chars: 17},
	), WasteThresholds{})
	if hasCat(single, WasteEmptySearch) {
		t.Fatalf("misses separated by other work are probes, not a streak: %+v", single.Categories)
	}

	sameArg := AnalyzeWaste(wasteRows(
		wcall{tool: "Grep", arg: "foo", chars: 17},
		wcall{tool: "Grep", arg: "foo", chars: 17},
	), WasteThresholds{})
	if hasCat(sameArg, WasteEmptySearch) {
		t.Fatalf("one pattern twice is a repeat, not guessing: %+v", sameArg.Categories)
	}
}

func TestEmptySearchThresholdAndNonEmptyBreaksStreak(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Grep", arg: "a", chars: 10},
		wcall{tool: "Grep", arg: "b", chars: 5000}, // real hits: ends the streak
		wcall{tool: "Grep", arg: "c", chars: 10},
	), WasteThresholds{})
	if hasCat(r, WasteEmptySearch) {
		t.Fatalf("a search with hits must break the streak: %+v", r.Categories)
	}
	custom := AnalyzeWaste(wasteRows(
		wcall{tool: "Grep", arg: "a", chars: 150},
		wcall{tool: "Grep", arg: "b", chars: 150},
	), WasteThresholds{EmptySearchChars: 200})
	if findCat(t, custom, WasteEmptySearch).Count != 2 {
		t.Fatalf("overridden threshold not applied: %+v", custom.Categories)
	}
}

func TestOversizedReadCountsOnlyExcess(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "huge.go", chars: 4 * (DefaultOversizedReadTokens + 500)},
		wcall{tool: "Read", arg: "ok.go", chars: 4 * (DefaultOversizedReadTokens - 1)},
		wcall{tool: "Bash", arg: "cat huge.txt", chars: 4 * (DefaultOversizedReadTokens + 500)},
	), WasteThresholds{})
	c := findCat(t, r, WasteOversizedRead)
	if c.Count != 1 || c.EstTokens != 500 {
		t.Fatalf("count=%d tokens=%d, want 1 and the 500 over the threshold", c.Count, c.EstTokens)
	}
	custom := AnalyzeWaste(wasteRows(wcall{tool: "Read", arg: "m.go", chars: 4000}), WasteThresholds{OversizedReadTokens: 100})
	if findCat(t, custom, WasteOversizedRead).EstTokens != 900 {
		t.Fatalf("overridden threshold not applied: %+v", custom.Categories)
	}
}

func TestRerunCmdNeedsNoEditBetween(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Bash", arg: "go test ./...", chars: 400},
		wcall{tool: "Read", arg: "a.go", chars: 400},
		wcall{tool: "Bash", arg: "go test ./...", chars: 400},
		wcall{tool: "Edit", arg: "a.go", chars: 20},
		wcall{tool: "Bash", arg: "go test ./...", chars: 400}, // after an edit: fine
		wcall{tool: "Bash", arg: "go vet ./...", chars: 40},
	), WasteThresholds{})
	c := findCat(t, r, WasteRerunCmd)
	if c.Count != 1 || c.Examples[0].Step != 2 {
		t.Fatalf("rerun = %+v", c)
	}
}

func TestRerunCmdSkipsFailedAndEmpty(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Bash", arg: "go build", chars: 90, err: true},
		wcall{tool: "Bash", arg: "go build", chars: 10},
		wcall{tool: "Bash", arg: "", chars: 0},
		wcall{tool: "Bash", arg: "  ", chars: 0},
	), WasteThresholds{})
	if hasCat(r, WasteRerunCmd) {
		t.Fatalf("retry after a failure and blank commands are not reruns: %+v", r.Categories)
	}
}

func TestCleanRunGivesEmptyReport(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "a.go", chars: 800},
		wcall{tool: "Edit", arg: "a.go", chars: 20},
		wcall{tool: "Bash", arg: "go test ./...", chars: 200},
	), WasteThresholds{})
	if len(r.Categories) != 0 || r.Categories == nil {
		t.Fatalf("a clean run must give an empty, non-nil category list: %#v", r.Categories)
	}
	if r.WastedCalls != 0 || r.WastedLifetimeTokens != 0 || r.WastedShare != 0 {
		t.Fatalf("nothing wasted, got %+v", r)
	}
	if r.TotalCalls != 3 || r.TotalLifetimeTokens == 0 {
		t.Fatalf("totals must still describe the window: %+v", r)
	}
	empty := AnalyzeWaste(nil, WasteThresholds{})
	if empty.TotalCalls != 0 || len(empty.Categories) != 0 || empty.Categories == nil {
		t.Fatalf("no rows: %+v", empty)
	}
}

func TestZeroResultCharsDoNotBreakShares(t *testing.T) {
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "a.go", chars: 0},
		wcall{tool: "Read", arg: "a.go", chars: 0},
		wcall{tool: "Bash", arg: "ls", chars: 0},
		wcall{tool: "Bash", arg: "ls", chars: 0},
	), WasteThresholds{})
	c := findCat(t, r, WasteRepeatRead)
	if c.Count != 1 || c.EstTokens != 0 || c.Share != 0 {
		t.Fatalf("zero-size repeat: %+v", c)
	}
	if r.WastedShare != 0 || r.TotalLifetimeTokens != 0 {
		t.Fatalf("no volume, no share: %+v", r)
	}
}

func TestWasteWeightedByRemainingSteps(t *testing.T) {
	// The same 4000-char repeat costs more early in a run than at its end:
	// every later step re-reads it.
	early := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "a.go", chars: 4000},
		wcall{tool: "Read", arg: "a.go", chars: 4000},
		wcall{tool: "Bash", arg: "x", chars: 4},
		wcall{tool: "Bash", arg: "y", chars: 4},
		wcall{tool: "Bash", arg: "z", chars: 4},
	), WasteThresholds{})
	late := AnalyzeWaste(wasteRows(
		wcall{tool: "Bash", arg: "x", chars: 4},
		wcall{tool: "Bash", arg: "y", chars: 4},
		wcall{tool: "Bash", arg: "z", chars: 4},
		wcall{tool: "Read", arg: "a.go", chars: 4000},
		wcall{tool: "Read", arg: "a.go", chars: 4000},
	), WasteThresholds{})
	e, l := findCat(t, early, WasteRepeatRead), findCat(t, late, WasteRepeatRead)
	// 1000 tokens × (2 + 0.1×3) = 2300 and 1000 × 2 = 2000.
	if e.LifetimeTokens != 2300 || l.LifetimeTokens != 2000 {
		t.Fatalf("lifetime tokens early=%d late=%d, want 2300 and 2000", e.LifetimeTokens, l.LifetimeTokens)
	}
	if e.Share <= l.Share || e.Share <= 0 || e.Share >= 1 {
		t.Fatalf("shares early=%v late=%v", e.Share, l.Share)
	}
}

func TestWasteCallFlaggedTwiceCountsOnceInTotals(t *testing.T) {
	big := 4 * (DefaultOversizedReadTokens + 1000)
	r := AnalyzeWaste(wasteRows(
		wcall{tool: "Read", arg: "huge.go", chars: big},
		wcall{tool: "Read", arg: "huge.go", chars: big},
	), WasteThresholds{})
	if findCat(t, r, WasteRepeatRead).Count != 1 || findCat(t, r, WasteOversizedRead).Count != 2 {
		t.Fatalf("categories: %+v", r.Categories)
	}
	if r.WastedCalls != 2 {
		t.Fatalf("WastedCalls = %d, want 2 distinct calls", r.WastedCalls)
	}
	if r.WastedShare <= 0 || r.WastedShare > 1 {
		t.Fatalf("WastedShare = %v", r.WastedShare)
	}
}

func TestWasteExamplesCappedAndSortedByCost(t *testing.T) {
	var calls []wcall
	calls = append(calls, wcall{tool: "Bash", arg: "make", chars: 4})
	for i := 0; i < 8; i++ {
		calls = append(calls, wcall{tool: "Bash", arg: "make", chars: 4 * (i + 1) * 100})
	}
	r := AnalyzeWaste(wasteRows(calls...), WasteThresholds{})
	c := findCat(t, r, WasteRerunCmd)
	if c.Count != 8 || len(c.Examples) != 5 {
		t.Fatalf("count=%d examples=%d", c.Count, len(c.Examples))
	}
	for i := 1; i < len(c.Examples); i++ {
		if c.Examples[i].LifetimeTokens > c.Examples[i-1].LifetimeTokens {
			t.Fatalf("examples not sorted by cost: %+v", c.Examples)
		}
	}
}

func TestWasteRunsAreIndependent(t *testing.T) {
	a, b := int64(1), int64(2)
	rows := []store.ActionRow{
		{ID: 1, Session: "P1", RunID: &a, StepIndex: 0, Tool: "Read", Arg: "a.go", ResultChars: 400},
		{ID: 2, Session: "P1", RunID: &b, StepIndex: 0, Tool: "Read", Arg: "a.go", ResultChars: 400},
	}
	if r := AnalyzeWaste(rows, WasteThresholds{}); hasCat(r, WasteRepeatRead) {
		t.Fatalf("the same read in two different runs is not a repeat: %+v", r.Categories)
	}
	// A resumed CLI session spans runs: its context carries over.
	rows[0].CLISessionID, rows[1].CLISessionID = "cli-1", "cli-1"
	rows[1].StepIndex = 1
	if r := AnalyzeWaste(rows, WasteThresholds{}); !hasCat(r, WasteRepeatRead) {
		t.Fatalf("a repeat inside one CLI session must be found: %+v", r.Categories)
	}
}

func TestBuildWasteReportFromStore(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	var rows []store.ActionRow
	for i, c := range []wcall{
		{tool: "Read", arg: "a.go", chars: 4000},
		{tool: "Read", arg: "a.go", chars: 4000},
		{tool: "Bash", arg: "ls", chars: 40, err: true},
	} {
		rows = append(rows, store.ActionRow{
			Project: "p", Session: "P1", StepIndex: i, Tool: c.tool, Sig: c.tool + ":x", Arg: c.arg,
			IsError: c.err, ResultChars: c.chars, Timestamp: now,
		})
	}
	rows = append(rows, store.ActionRow{Project: "other", Session: "P1", Tool: "Read", Sig: "Read:x", Arg: "a.go", ResultChars: 4000, Timestamp: now})
	rows = append(rows, store.ActionRow{Project: "p", Session: "P1", StepIndex: 9, Tool: "Read", Sig: "Read:x", Arg: "a.go", ResultChars: 4000, Timestamp: now.AddDate(0, 0, -(attributionWindowDays + 5))})
	if err := st.InsertActions(rows); err != nil {
		t.Fatal(err)
	}
	r, err := BuildWasteReport(st, "p", WasteThresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if r.TotalCalls != 3 {
		t.Fatalf("TotalCalls = %d: other projects and rows outside the window must stay out", r.TotalCalls)
	}
	if c := findCat(t, r, WasteRepeatRead); c.Count != 1 {
		t.Fatalf("repeat = %+v", c)
	}
}
