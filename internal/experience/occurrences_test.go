package experience

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/store"
)

func TestMineCandidates_FailureSigNotInCandidateNotRelated(t *testing.T) {
	ts := time.Now()
	var runs []CandidateRun
	for _, k := range []string{"r1", "r2", "r3"} {
		runs = append(runs, run(k, "completed", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)))
	}
	foreign := FailureCluster{ErrorKey: "foreign", Examples: []FixPair{{RunKey: "r1", Sig: "Z"}}}
	own := FailureCluster{ErrorKey: "own", Examples: []FixPair{{RunKey: "other", Sig: "B"}}}
	srcA := FailureCluster{ErrorKey: "gate", Examples: []FixPair{{RunKey: "r1"}}}
	c := findCandidate(MineCandidates(runs, DefaultMinRunShare, []FailureCluster{foreign, own, srcA}), "A", "B")
	if c == nil {
		t.Fatal("no [A B] candidate")
	}
	if len(c.RelatedFailures) != 1 || c.RelatedFailures[0].ErrorKey != "own" {
		t.Errorf("RelatedFailures = %+v, want only the cluster whose Sig is in the candidate", c.RelatedFailures)
	}
}

func TestMineCandidates_OccurrencesCarryAllRowsAndBestFirst(t *testing.T) {
	base := time.Now()
	mk := func(k, status string, off int) CandidateRun {
		ts := base.Add(time.Duration(off) * time.Hour)
		return run(k, status, row("A", "Bash", "a-"+k, 0, 1, ts), row("B", "Read", "b-"+k, 1, 1, ts))
	}
	runs := []CandidateRun{
		mk("err", "error", 9), // newest, but lowest weight
		mk("old", "completed", 1),
		mk("new", "completed", 2),
	}
	c := findCandidate(MineCandidates(runs, DefaultMinRunShare, nil), "A", "B")
	if c == nil {
		t.Fatal("no [A B] candidate")
	}
	if len(c.Occurrences) != 3 || len(c.Samples) != 3 {
		t.Fatalf("occurrences=%d samples=%d, want 3", len(c.Occurrences), len(c.Samples))
	}
	var order []string
	for i, occ := range c.Occurrences {
		if len(occ) != 2 {
			t.Fatalf("occurrence %d has %d rows, want 2", i, len(occ))
		}
		if c.Samples[i].Arg != occ[0].Arg {
			t.Errorf("Samples[%d] != Occurrences[%d][0]", i, i)
		}
		order = append(order, strings.TrimPrefix(occ[0].Arg, "a-"))
	}
	if !reflect.DeepEqual(order, []string{"new", "old", "err"}) {
		t.Errorf("order = %v, want [new old err]", order)
	}
}

func TestMineCandidates_OccurrencesBoundedEvictsWorst(t *testing.T) {
	base := time.Now()
	var runs []CandidateRun
	runs = append(runs, run("err", "error", row("A", "Bash", "a-err", 0, 1, base.Add(99*time.Hour)), row("B", "Bash", "b", 1, 1, base)))
	for i := 0; i < maxCandidateSamples; i++ {
		ts := base.Add(time.Duration(i) * time.Hour)
		runs = append(runs, run("c"+string(rune('0'+i)), "completed", row("A", "Bash", "a-c", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)))
	}
	c := findCandidate(MineCandidates(runs, DefaultMinRunShare, nil), "A", "B")
	if c == nil {
		t.Fatal("no [A B] candidate")
	}
	if len(c.Occurrences) != maxCandidateSamples {
		t.Fatalf("len = %d, want %d", len(c.Occurrences), maxCandidateSamples)
	}
	for _, occ := range c.Occurrences {
		if occ[0].Arg == "a-err" {
			t.Error("error-run occurrence should have been evicted by completed ones")
		}
	}
	if !c.Occurrences[0][0].Timestamp.After(c.Occurrences[1][0].Timestamp) {
		t.Error("equal weight: newer must come first")
	}
}

func TestSkillCandidate_OccurrencesJSONRoundTrip(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	c := SkillCandidate{Sig: []string{"A", "B"}, Occurrences: [][]store.ActionRow{{row("A", "Bash", "a", 0, 1, ts), row("B", "Read", "b", 1, 1, ts)}}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var back SkillCandidate
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Occurrences) != 1 || len(back.Occurrences[0]) != 2 || back.Occurrences[0][1].Arg != "b" {
		t.Errorf("round trip lost occurrences: %+v", back.Occurrences)
	}
	in := SkillDistillInputFromCandidate(back, nil)
	if len(in.Samples) != 1 || in.Samples[0].Command != "a\nRead b" {
		t.Errorf("distill sample = %+v", in.Samples)
	}
}

func TestExtractFromRun_FillsSig(t *testing.T) {
	ts := time.Now()
	f := row("S", "Bash", "x", 0, 1, ts)
	f.IsError = true
	pairs := ExtractFromRun([]store.ActionRow{f, row("S", "Bash", "y", 1, 1, ts)}, "r")
	if len(pairs) != 1 || pairs[0].Sig != "S" {
		t.Errorf("pairs = %+v", pairs)
	}
}
