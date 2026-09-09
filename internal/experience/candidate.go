package experience

import (
	"math"
	"sort"
	"strings"
	"time"

	"claude-manager/internal/store"
)

// DefaultMinRunShare is the fraction of a project's runs a candidate must
// appear in before it is worth distilling (LEARN-TASKS.md LN-08). A relative
// threshold, not an absolute count: measured on the corpus, an absolute
// `min_runs = 3` yielded 719 signatures across 5654 runs — two orders of
// magnitude more than makes sense to distill — while 5% keeps the shortlist
// to ~30-40 candidates on a large project and ~15 on a small one.
const DefaultMinRunShare = 0.05

// MinCandidateRuns is the absolute floor under DefaultMinRunShare: on a small
// project 5% of runs can be under 1, and a pattern seen in fewer than 3 runs
// is an anecdote, not a candidate.
const MinCandidateRuns = 3

// loopThreshold identical (tool, arg) calls within one run is a loop — the
// same threshold optimization.LoopDetector uses, matching tool+input exactly
// (LEARN-TASKS.md LN-08).
const loopThreshold = 3

// maxNGram is the longest tool-call sequence considered as a candidate
// (LEARN-TASKS.md LN-08: n-grams of 2..4 signatures, plus the bare 1-gram).
const maxNGram = 4

// maxCandidateSamples caps how many concrete occurrences a SkillCandidate
// keeps — enough for LN-09's distillation prompt ("до 5 реальных примеров")
// without shipping every occurrence ever seen.
const maxCandidateSamples = 5

// CandidateRun is one run's ordered action rows plus its outcome — the unit
// MineCandidates slides n-grams over. Key mirrors action_signatures'
// COALESCE(run_id, cli_session_id) grouping (see failures.go's
// actionRunKey), so a FailureCluster's FixPair.RunKey lines up with it for
// RelatedFailures matching. Status is the run's session_runs.Status; empty
// means unresolvable — a bulk-imported row (LN-17) has no run_id and so no
// status to look up, which MineCandidates treats as neutral evidence, not as
// evidence of success.
type CandidateRun struct {
	Key    string
	Status string
	Rows   []store.ActionRow
}

// SkillCandidate is a normalized tool-call sequence (a single signature or an
// n-gram of 2..4) that recurs across enough of a project's runs to be worth
// distilling into a skill (LEARN-TASKS.md LN-08).
type SkillCandidate struct {
	// Sig is the ordered signature sequence — length 1..maxNGram.
	Sig []string
	// DistinctRuns is the raw (outcome-unweighted) count of runs the sequence
	// was observed in at least once — the frequency measure the run-share
	// threshold and the UI's "seen in N runs" line use. A run where the
	// sequence repeats (a loop) still counts once here.
	DistinctRuns int
	// RunShare is DistinctRuns / total runs passed to MineCandidates.
	RunShare float64
	// Score ranks candidates for the distiller (LN-09); see scoreCandidate.
	Score float64
	// Samples is up to maxCandidateSamples concrete occurrences, one row per
	// distinct run (the row at the sequence's first occurrence in that run).
	Samples []store.ActionRow
	// RelatedFailures are failure clusters (LN-07) observed in a run this
	// candidate also appears in — see relateFailures for the matching rule.
	RelatedFailures []FailureCluster
	// ContextLossSuspect is true when this exact sequence was, in at least
	// one contributing run, a loop — the same single (tool, arg) call
	// repeated loopThreshold+ times identically. Such a run still counts
	// once toward DistinctRuns, but the candidate is flagged rather than
	// promoted silently: a re-read symptom is not a workflow habit.
	ContextLossSuspect bool
	// Imported is true when at least one contributing run has no resolvable
	// status (a bulk-imported row, LN-17) — the candidate's evidence is
	// partly of unknown quality.
	Imported  bool
	FirstSeen time.Time
	LastSeen  time.Time
}

// outcomeWeight maps a run's status to how much its evidence counts for
// (LEARN-TASKS.md LN-08): a completed run is full evidence, a stopped one
// partial, an error run cannot teach a good pattern at all, and an unknown
// status (no run_id to look up — imported history) is neutral. rate_limited
// is not enumerated in LN-08 but is, like stopped, an interruption rather
// than a failure of the run's own doing, so it gets the same partial weight.
func outcomeWeight(status string) float64 {
	switch status {
	case "completed":
		return 1.0
	case "stopped", "rate_limited":
		return 0.3
	case "error":
		return 0.0
	default: // "" (unknown/imported) or anything unrecognized
		return 0.6
	}
}

// candAcc accumulates one n-gram's evidence across runs while scanning.
type candAcc struct {
	sig          []string
	runKeys      []string // one entry per distinct run, in first-seen order — RelatedFailures matching
	distinctRuns int
	weightSum    float64 // Σ outcomeWeight(run) over distinct runs
	rediscovery  float64 // Σ charsToFirst(run) * outcomeWeight(run) over distinct runs
	suspect      bool
	imported     bool
	firstSeen    time.Time
	lastSeen     time.Time
	samples      []store.ActionRow
}

// MineCandidates extracts recurring tool-call sequences from runs — single
// signatures and contiguous n-grams of 2..maxNGram — that appear in at least
// MinCandidateRuns runs and at least minRunShare of the total (pass <= 0 for
// DefaultMinRunShare). clusters links each result's RelatedFailures.
//
// Score, per candidate, is `distinctRuns * log(1+rediscoveryChars) *
// outcomeWeight` (LEARN-TASKS.md LN-08) computed as weightSum *
// log(1+rediscoveryChars): outcomeWeight there is the *average* per-run
// weight, so distinctRuns * average = weightSum exactly, and both
// rediscoveryChars and weightSum already accumulate each run's own weight.
// This makes the "a failed run must not pull the candidate up" invariant
// exact rather than approximate: a run with outcomeWeight 0 (status=error)
// contributes literally 0 to both terms, so adding one to a candidate's
// evidence never changes its Score — DistinctRuns still grows (it is an
// honest, unweighted occurrence count), but the score that ranks candidates
// for distillation does not move.
func MineCandidates(runs []CandidateRun, minRunShare float64, clusters []FailureCluster) []SkillCandidate {
	if minRunShare <= 0 {
		minRunShare = DefaultMinRunShare
	}
	totalRuns := len(runs)
	if totalRuns == 0 {
		return nil
	}

	accs := make(map[string]*candAcc)
	var order []string

	for _, run := range runs {
		rows := append([]store.ActionRow(nil), run.Rows...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].StepIndex < rows[j].StepIndex })

		loopSigs := loopSignatures(rows)
		weight := outcomeWeight(run.Status)
		seenInRun := make(map[string]bool)

		for n := 1; n <= maxNGram; n++ {
			for i := 0; i+n <= len(rows); i++ {
				gram := make([]string, n)
				for k := 0; k < n; k++ {
					gram[k] = rows[i+k].Sig
				}
				key := strings.Join(gram, "\x1f")
				if seenInRun[key] {
					continue // only the first occurrence in a run counts
				}
				seenInRun[key] = true

				acc, ok := accs[key]
				if !ok {
					acc = &candAcc{sig: gram}
					accs[key] = acc
					order = append(order, key)
				}
				acc.runKeys = append(acc.runKeys, run.Key)
				acc.distinctRuns++
				acc.weightSum += weight

				var charsToFirst int64
				for _, r := range rows[:i] {
					charsToFirst += int64(r.ResultChars)
				}
				acc.rediscovery += float64(charsToFirst) * weight

				if run.Status == "" {
					acc.imported = true
				}
				if uniformGram(gram) && loopSigs[gram[0]] {
					acc.suspect = true
				}

				ts := rows[i].Timestamp
				if acc.firstSeen.IsZero() || ts.Before(acc.firstSeen) {
					acc.firstSeen = ts
				}
				if ts.After(acc.lastSeen) {
					acc.lastSeen = ts
				}
				if len(acc.samples) < maxCandidateSamples {
					acc.samples = append(acc.samples, rows[i])
				}
			}
		}
	}

	// Frequency filter.
	var kept []string
	for _, key := range order {
		acc := accs[key]
		share := float64(acc.distinctRuns) / float64(totalRuns)
		if acc.distinctRuns >= MinCandidateRuns && share >= minRunShare {
			kept = append(kept, key)
		}
	}

	// Nested dedup: drop a shorter n-gram fully contained (as a contiguous
	// subsequence) in a longer kept n-gram with the identical DistinctRuns —
	// wherever the short pattern appears, so does the long one, so the long
	// one is strictly more informative (LEARN-TASKS.md LN-08).
	dropped := make(map[string]bool)
	for _, shortKey := range kept {
		short := accs[shortKey]
		for _, longKey := range kept {
			if shortKey == longKey {
				continue
			}
			long := accs[longKey]
			if len(long.sig) <= len(short.sig) {
				continue
			}
			if long.distinctRuns != short.distinctRuns {
				continue
			}
			if containsContiguous(long.sig, short.sig) {
				dropped[shortKey] = true
				break
			}
		}
	}

	out := make([]SkillCandidate, 0, len(kept))
	for _, key := range kept {
		if dropped[key] {
			continue
		}
		acc := accs[key]
		out = append(out, SkillCandidate{
			Sig:                acc.sig,
			DistinctRuns:       acc.distinctRuns,
			RunShare:           float64(acc.distinctRuns) / float64(totalRuns),
			Score:              acc.weightSum * math.Log(1+acc.rediscovery),
			Samples:            acc.samples,
			RelatedFailures:    relateFailures(acc.runKeys, clusters),
			ContextLossSuspect: acc.suspect,
			Imported:           acc.imported,
			FirstSeen:          acc.firstSeen,
			LastSeen:           acc.lastSeen,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].DistinctRuns != out[j].DistinctRuns {
			return out[i].DistinctRuns > out[j].DistinctRuns
		}
		return strings.Join(out[i].Sig, "\x1f") < strings.Join(out[j].Sig, "\x1f")
	})
	return out
}

// loopSignatures returns the set of signatures that, within this one run,
// belong to a (tool, arg) pair repeated loopThreshold+ times identically —
// the same "3+ identical tool+input" rule optimization.LoopDetector applies
// live, mined here after the fact from a run's own rows (LEARN-TASKS.md
// LN-08). A signature can have multiple distinct args; only args that
// literally repeat make it loopy.
func loopSignatures(rows []store.ActionRow) map[string]bool {
	counts := make(map[string]int)
	sigOf := make(map[string]string)
	for _, r := range rows {
		k := r.Tool + "\x00" + r.Arg
		counts[k]++
		sigOf[k] = r.Sig
	}
	loopy := make(map[string]bool)
	for k, c := range counts {
		if c >= loopThreshold {
			loopy[sigOf[k]] = true
		}
	}
	return loopy
}

// uniformGram reports whether every element of gram is the same signature —
// only a uniform gram (including the bare 1-gram) can be "the same call
// repeated", which is what a loop is.
func uniformGram(gram []string) bool {
	for _, s := range gram[1:] {
		if s != gram[0] {
			return false
		}
	}
	return true
}

// containsContiguous reports whether short appears as a contiguous
// subsequence of long.
func containsContiguous(long, short []string) bool {
	if len(short) > len(long) {
		return false
	}
	for i := 0; i+len(short) <= len(long); i++ {
		match := true
		for k, s := range short {
			if long[i+k] != s {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// relateFailures links a candidate to the failure clusters (LN-07) observed
// in a run it also appears in. Matching is by run key against each cluster's
// kept Examples only (FailureCluster caps Examples at 5 — the underlying
// per-cluster run set isn't otherwise exposed), so a large cluster whose
// matching example got capped away can be missed; that is an acceptable
// under-match for a "here's a related failure" hint, never a correctness
// requirement.
func relateFailures(runKeys []string, clusters []FailureCluster) []FailureCluster {
	if len(clusters) == 0 || len(runKeys) == 0 {
		return nil
	}
	runSet := make(map[string]bool, len(runKeys))
	for _, k := range runKeys {
		runSet[k] = true
	}
	var related []FailureCluster
	for _, c := range clusters {
		for _, ex := range c.Examples {
			if ex.RunKey != "" && runSet[ex.RunKey] {
				related = append(related, c)
				break
			}
		}
	}
	return related
}
