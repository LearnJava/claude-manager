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
	// Kind is what this candidate is actually worth turning into — see
	// classifyCandidate. Advisory: nothing refuses to distill a
	// KindPermission/KindNoise candidate, but the ranking and the automatic
	// threshold (ResolveSkillMinScore) both treat KindSkill as the real
	// population.
	Kind CandidateKind
	// KindReason is a stable code explaining Kind (reasonMultiStep,
	// reasonKnownFailure, reasonReadOnly, reasonLoop, reasonSingleStep) —
	// a code, not prose, because the UI is localized (frontend/src/lib/i18n.ts).
	KindReason string
}

// CandidateKind is what a mined candidate should become. Frequency alone
// cannot answer that: a single recurring command is real evidence of
// *something*, but a skill is a procedure — an order of steps and the
// conditions around them — and a lone `Bash:ls <ARG>` has no order to teach.
// Measured on a real corpus, the top of the ranked list was entirely
// 1-grams (`sed -n`, `git status --short`, `grep -n`, `ls`) precisely
// because a single signature occurs in at least as many runs as every
// n-gram containing it, so it necessarily outranks the sequences it is part
// of. Rather than drop 1-grams from mining (they carry the frequency
// evidence the run-share threshold is built on, and one *with a known
// failure attached* is among the best skills there is), each candidate is
// labelled with what it is good for.
type CandidateKind string

const (
	// KindSkill — worth distilling into a SKILL.md (LN-09).
	KindSkill CandidateKind = "skill"
	// KindPermission — a single read-only call; the actionable win is an
	// auto-allow rule (LN-04's Permissions tab), not a procedure.
	KindPermission CandidateKind = "permission"
	// KindNoise — the same call repeated identically: a context-loss
	// symptom, not a habit worth teaching back.
	KindNoise CandidateKind = "noise"
)

// KindReason codes — see SkillCandidate.KindReason.
const (
	reasonMultiStep    = "multi_step"
	reasonKnownFailure = "known_failure"
	reasonReadOnly     = "read_only"
	reasonLoop         = "loop"
	reasonSingleStep   = "single_step"
)

// kindRank orders KindSkill first, then KindPermission, then KindNoise — see
// MineCandidates' sort.
func kindRank(k CandidateKind) int {
	switch k {
	case KindSkill:
		return 0
	case KindPermission:
		return 1
	default:
		return 2
	}
}

// classifyCandidate is a hard rule set, never a heuristic score — the same
// stance ClassifyPermission (LN-04) takes, and for the same reason: a
// guessed verdict that reads as authoritative is worse than no verdict.
//
//  1. A uniform gram flagged as a loop is noise, whatever its length: the
//     identical call repeated is the context-loss symptom ContextLossSuspect
//     already names, and distilling it would teach the symptom back.
//  2. Any sequence of 2+ distinct steps is a skill: the order is the content,
//     and only a procedure can carry it.
//  3. A single step with a related failure cluster (LN-07) is a skill too —
//     "this command fails like this, fix it like that" is the highest-value
//     thing this whole pipeline produces, and it needs no second step.
//  4. A single step whose every observed sample is read-only
//     (ClassifyPermission) belongs in the Permissions tab instead: the win
//     there is one fewer permission prompt per run, which a skill cannot
//     deliver.
//  5. Anything else single-step (a write, a non-whitelisted command) stays a
//     skill candidate, ranked below the sequences.
//
// Rule 4 classifies the *samples'* verbatim args, never the signature: a
// signature is masked (`sed -n <ARG> <ARG>`) and the mask's own angle
// brackets trip ClassifyPermission's redirection check, so every masked
// signature would come back unsafe. Every sample must pass — one unsafe
// observation is enough to keep the candidate out of an allow-rule
// suggestion.
func classifyCandidate(sig []string, samples []store.ActionRow, related []FailureCluster, loopSuspect bool) (CandidateKind, string) {
	if loopSuspect && uniformGram(sig) {
		return KindNoise, reasonLoop
	}
	if len(sig) > 1 {
		return KindSkill, reasonMultiStep
	}
	if len(related) > 0 {
		return KindSkill, reasonKnownFailure
	}
	if allSamplesReadOnly(samples) {
		return KindPermission, reasonReadOnly
	}
	return KindSkill, reasonSingleStep
}

// allSamplesReadOnly reports whether every sample occurrence of a candidate
// is cleared by ClassifyPermission. No samples at all is not read-only —
// absence of evidence never clears anything here.
func allSamplesReadOnly(samples []store.ActionRow) bool {
	if len(samples) == 0 {
		return false
	}
	for _, s := range samples {
		if !ClassifyPermission(s.Tool, s.Arg) {
			return false
		}
	}
	return true
}

// outcomeWeight maps a run's status to how much a *successful* step's
// evidence counts for (LEARN-TASKS.md LN-22, superseding LN-08's original
// run-level formula): a completed run is full evidence, everything else that
// didn't cleanly finish (stopped, rate-limited, or the run eventually errored
// out) is partial, and an unknown status (no run_id to look up — imported
// history) is neutral. This is a *step*-level weight now, applied only to a
// gram occurrence whose own rows are error-free — see stepOccurrenceWeight,
// which is what actually zeroes out a failed step's contribution.
//
// LN-08 originally gave status="error" a hard 0.0 — on real data (Lumen:
// 7593 of 9270 runs are status=error) that meant the overwhelming majority of
// a project's history could never contribute at all, even when the run's
// first ninety-nine steps succeeded and only the hundredth crashed it. From
// one already-failed step's own perspective, an "error" run is exactly like
// "stopped"/"rate_limited": the run itself didn't reach a clean finish, but
// that says nothing about whether *this particular* successful step is a
// reliable observation — so error now shares stopped/rate_limited's reduced
// (not zero) weight.
func outcomeWeight(status string) float64 {
	switch status {
	case "completed", "slice":
		return 1.0
	case "stopped", "rate_limited", "error", "unfinished":
		return 0.3
	default: // "" (unknown/imported) or anything unrecognized
		return 0.6
	}
}

// stepOccurrenceWeight is the weight of one gram occurrence: rows is the
// slice of contiguous action rows the gram spans at this occurrence. If any
// of them is itself an error (IsError), the occurrence contributes nothing —
// "n-грамма, у которой ошибочны сами шаги, по-прежнему не набирает очков"
// (LEARN-TASKS.md LN-22) — regardless of what the surrounding run's overall
// status was: a step that failed is not a reliable observation no matter how
// the run around it ended. Otherwise the occurrence counts at the run's own
// outcomeWeight — a genuine, successful step is real evidence even inside a
// run that later errored out.
func stepOccurrenceWeight(rows []store.ActionRow, status string) float64 {
	for _, r := range rows {
		if r.IsError {
			return 0
		}
	}
	return outcomeWeight(status)
}

// candAcc accumulates one n-gram's evidence across runs while scanning.
type candAcc struct {
	sig          []string
	runKeys      []string // one entry per distinct run, in first-seen order — RelatedFailures matching
	distinctRuns int
	weightSum    float64 // Σ stepOccurrenceWeight(occurrence) over distinct runs
	rediscovery  float64 // Σ charsToFirst(run) * stepOccurrenceWeight(occurrence) over distinct runs
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
// outcomeWeight` (LEARN-TASKS.md LN-08/LN-22) computed as weightSum *
// log(1+rediscoveryChars): outcomeWeight there is the *average* per-run
// weight, so distinctRuns * average = weightSum exactly, and both
// rediscoveryChars and weightSum already accumulate each occurrence's own
// weight — now per gram occurrence (stepOccurrenceWeight), not once for the
// whole run (LN-22: weighting by run status alone meant a run that failed on
// its hundredth step contributed nothing for the ninety-nine steps it got
// right). This keeps the "a failed step must not pull the candidate up"
// invariant exact rather than approximate: an occurrence whose own rows
// include an error contributes literally 0 to both terms, so adding one to a
// candidate's evidence never changes its Score — DistinctRuns still grows (it is an
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

				weight := stepOccurrenceWeight(rows[i:i+n], run.Status)

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
		related := relateFailures(acc.runKeys, clusters)
		kind, reason := classifyCandidate(acc.sig, acc.samples, related, acc.suspect)
		out = append(out, SkillCandidate{
			Sig:                acc.sig,
			DistinctRuns:       acc.distinctRuns,
			RunShare:           float64(acc.distinctRuns) / float64(totalRuns),
			Score:              acc.weightSum * math.Log(1+acc.rediscovery),
			Samples:            acc.samples,
			RelatedFailures:    related,
			ContextLossSuspect: acc.suspect,
			Imported:           acc.imported,
			FirstSeen:          acc.firstSeen,
			LastSeen:           acc.lastSeen,
			Kind:               kind,
			KindReason:         reason,
		})
	}

	// Kind before Score: a 1-gram necessarily occurs in at least as many runs
	// as every sequence containing it, so ranking by Score alone puts the
	// alphabet above the workflows (see CandidateKind). Score still orders
	// within a kind, and is left untouched as a number — LN-22's weighting
	// invariants and LN-23's relative threshold are both defined on it.
	sort.Slice(out, func(i, j int) bool {
		if ri, rj := kindRank(out[i].Kind), kindRank(out[j].Kind); ri != rj {
			return ri < rj
		}
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

// DefaultSkillTopFraction is the fraction of a project's mined skill
// candidates that clear the distillation threshold when no explicit minScore
// is given (LEARN-TASKS.md LN-23) — a share, not an absolute score, for the
// same reason DefaultMinRunShare above is a share rather than a count: Score
// scales with weightSum*log(1+rediscoveryChars), which grows with corpus
// size, so a fixed cutoff (the old analysis.DefaultSkillMinScore=10.0) cut
// *everything* on this app's own small DB (max score 6.9 of 24 candidates)
// and let *almost everything* through on an imported corpus two orders of
// magnitude larger (378 of 379, p50=782) — an absolute number over a
// corpus-size-dependent quantity cannot work in both directions at once. A
// fixed top fraction of the *current* distribution clears a comparable share
// of candidates regardless of how large that distribution is.
const DefaultSkillTopFraction = 0.03

// RelativeScoreThreshold returns the Score a candidate must clear to be
// among the top topFraction of scores (topFraction <= 0 falls back to
// DefaultSkillTopFraction). scores need not be pre-sorted. An empty slice
// returns 0 — there is nothing to threshold against. The returned value
// always equals some element of scores when scores is non-empty, so the
// highest-scoring candidate always clears it regardless of how small
// topFraction*len(scores) rounds down to — a project with only one or two
// candidates never loses all of them to rounding.
func RelativeScoreThreshold(scores []float64, topFraction float64) float64 {
	if topFraction <= 0 {
		topFraction = DefaultSkillTopFraction
	}
	if len(scores) == 0 {
		return 0
	}
	sorted := append([]float64(nil), scores...)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
	idx := int(math.Ceil(float64(len(sorted))*topFraction)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// ResolveSkillMinScore picks the threshold DistillSkill should compare a
// candidate's score against (LEARN-TASKS.md LN-23): an explicit minScore
// (> 0 — a caller/UI override) always wins outright; with none given it
// falls back to RelativeScoreThreshold over the project's *current*
// candidate distribution, so the cutoff tracks corpus size instead of being
// pinned to an uncalibrated absolute constant.
// The distribution it thresholds against is the KindSkill candidates only,
// when there are any: a top-N% cutoff is meaningless if the population it is
// a percentage of is mostly one-liners that should never be distilled in the
// first place (measured: the ranked list's own head was entirely
// KindPermission 1-grams). With no KindSkill candidate at all — including
// every caller that builds SkillCandidates without a Kind, e.g. a direct
// test — it falls back to the full list, so the threshold is never computed
// over an empty set.
func ResolveSkillMinScore(candidates []SkillCandidate, minScore, topFraction float64) float64 {
	if minScore > 0 {
		return minScore
	}
	scores := make([]float64, 0, len(candidates))
	for _, c := range candidates {
		if c.Kind == KindSkill {
			scores = append(scores, c.Score)
		}
	}
	if len(scores) == 0 {
		for _, c := range candidates {
			scores = append(scores, c.Score)
		}
	}
	return RelativeScoreThreshold(scores, topFraction)
}

// candidateWindowDays bounds MineProjectCandidates the same way
// TopSignatures/DurationProfile bound their own windows — mining should
// reflect the project's current workflow, not a pattern that only ever
// happened a year ago.
const candidateWindowDays = 90

// BuildCandidateRuns groups action rows sharing a run — COALESCE(run_id,
// cli_session_id), the same fallback actionRunKey already applies for LN-07
// — into the CandidateRun shape MineCandidates consumes, carrying each row's
// own run status along (identical for every row belonging to one run).
func BuildCandidateRuns(rows []store.CandidateActionRow) []CandidateRun {
	groups := make(map[string]*CandidateRun)
	var order []string
	for _, cr := range rows {
		key := actionRunKey(cr.ActionRow)
		g, ok := groups[key]
		if !ok {
			g = &CandidateRun{Key: key, Status: cr.RunStatus}
			groups[key] = g
			order = append(order, key)
		}
		g.Rows = append(g.Rows, cr.ActionRow)
	}
	out := make([]CandidateRun, 0, len(order))
	for _, key := range order {
		out = append(out, *groups[key])
	}
	return out
}

// MineProjectCandidates loads a project's recent action_signatures rows and
// mines them into ranked skill candidates (LEARN-TASKS.md LN-08) — the
// missing link between LN-02's ingestion and LN-09's DistillSkill, which has
// no other way to receive a SkillCandidate to distill. Failure clusters
// (LN-07, source B — ExtractFromActionRows) are mined from the same rows so
// RelatedFailures is populated without a second store round-trip. Returns
// nil, nil when the project has no ingested history yet, rather than an
// error — an empty candidate list is the normal, expected state for a
// project that just turned experience_tracking on.
func MineProjectCandidates(st *store.Store, project string) ([]SkillCandidate, error) {
	rows, err := st.ActionRowsForCandidates(project, candidateWindowDays)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	runs := BuildCandidateRuns(rows)

	plain := make([]store.ActionRow, len(rows))
	for i, r := range rows {
		plain[i] = r.ActionRow
	}
	clusters := Cluster(ExtractFromActionRows(plain))

	return MineCandidates(runs, 0, clusters), nil
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
