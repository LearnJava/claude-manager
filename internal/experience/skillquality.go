package experience

import (
	"encoding/json"
	"sort"
	"time"

	"claude-manager/internal/store"
)

// MinSkillEffectRuns is the "not enough data" floor per side of a
// before/after-approval comparison (LEARN-TASKS.md LN-11): fewer than this
// many comparable runs on either side yields no verdict in the UI, just an
// explicit "insufficient data" flag — a median of one or two runs is noise,
// not a measurement.
const MinSkillEffectRuns = 3

// StaleRunWindow is how many of a project's most recent runs are checked for
// whether a skill's signatures occur at all (LEARN-TASKS.md LN-11: "ни одна
// сигнатура не встретилась за последние 20 прогонов проекта").
const StaleRunWindow = 20

// StaleMinRunsAfter is the number of post-approval comparable runs required
// before "median tokens did not decrease" counts as a staleness signal
// (LEARN-TASKS.md LN-11: "при ≥5 прогонах после аппрува").
const StaleMinRunsAfter = 5

// Staleness reasons surfaced by SkillEffect.StaleReason.
const (
	StaleReasonUnused        = "unused"
	StaleReasonNoImprovement = "no_improvement"
)

// SkillStats is one side (before/after approval) of a SkillEffect
// comparison. Medians, not means — LEARN-TASKS.md LN-11 calls out that
// per-run cost is long-tailed, so a single outlier run must not dominate the
// summary.
type SkillStats struct {
	Runs              int     `json:"runs"`
	MedianInputTokens float64 `json:"median_input_tokens"`
	MedianNumTurns    float64 `json:"median_num_turns"`
	CompletedRate     float64 `json:"completed_rate"`
}

// SkillEffect is one skill's measured before/after-approval comparison
// (LEARN-TASKS.md LN-11), structured after worker/quality.go's ModelQuality:
// a plain aggregate report, not a live-updating metric.
type SkillEffect struct {
	SkillID    int64      `json:"skill_id"`
	SkillName  string     `json:"skill_name"`
	ApprovedAt time.Time  `json:"approved_at"`
	Before     SkillStats `json:"before"`
	After      SkillStats `json:"after"`
	// InsufficientData is true when Before.Runs or After.Runs is below
	// MinSkillEffectRuns — the UI must draw no conclusion from such a row.
	InsufficientData bool `json:"insufficient_data"`
	// Stale is a *suggestion* ("предложить в архив"), never an automatic
	// archive — ArchiveSkill (LN-10) stays a human's explicit action.
	Stale       bool   `json:"stale"`
	StaleReason string `json:"stale_reason,omitempty"` // "unused" | "no_improvement" | ""
}

// BuildSkillQualityReport measures the before/after-approval effect of every
// approved-or-later skill in project (LEARN-TASKS.md LN-11) and flags
// candidates for archival. A skill that was never approved (still draft, or
// archived before ever being approved) has no ApprovedAt to split runs on and
// is skipped — there is nothing to measure yet.
func BuildSkillQualityReport(st *store.Store, project string) ([]SkillEffect, error) {
	skills, err := st.ListSkills(project)
	if err != nil {
		return nil, err
	}
	runs, err := st.ListRuns(project, "", 0) // all runs, newest first
	if err != nil {
		return nil, err
	}
	recentWindow := runs
	if len(recentWindow) > StaleRunWindow {
		recentWindow = recentWindow[:StaleRunWindow]
	}

	var out []SkillEffect
	for _, sk := range skills {
		if sk.ApprovedAt == nil {
			continue
		}
		var sigs []string
		if err := json.Unmarshal([]byte(sk.SourceJSON), &sigs); err != nil || len(sigs) == 0 {
			// Malformed or empty source_json — nothing to measure this skill
			// against; skip it rather than fail the whole report.
			continue
		}
		runsWithSig, err := st.RunsWithSignature(project, sigs)
		if err != nil {
			return nil, err
		}

		eff := measureSkillEffect(sk, runs, runsWithSig)

		usedRecently := false
		for _, r := range recentWindow {
			if runsWithSig[r.ID] {
				usedRecently = true
				break
			}
		}
		evaluateStale(&eff, usedRecently)

		out = append(out, eff)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].SkillName < out[j].SkillName })
	return out, nil
}

// measureSkillEffect splits runs into before/after sk.ApprovedAt, restricted
// to the ones runsWithSig marks comparable (at least one step whose sig is in
// sk.SourceJSON), and computes SkillStats for each side.
func measureSkillEffect(sk store.Skill, runs []*store.SessionRun, runsWithSig map[int64]bool) SkillEffect {
	approvedAt := *sk.ApprovedAt
	eff := SkillEffect{SkillID: sk.ID, SkillName: sk.Name, ApprovedAt: approvedAt}

	var before, after []*store.SessionRun
	for _, r := range runs {
		if !runsWithSig[r.ID] {
			continue
		}
		if r.StartedAt.Before(approvedAt) {
			before = append(before, r)
		} else {
			after = append(after, r)
		}
	}

	eff.Before = computeSkillStats(before)
	eff.After = computeSkillStats(after)
	eff.InsufficientData = eff.Before.Runs < MinSkillEffectRuns || eff.After.Runs < MinSkillEffectRuns
	return eff
}

func computeSkillStats(runs []*store.SessionRun) SkillStats {
	if len(runs) == 0 {
		return SkillStats{}
	}
	tokens := make([]float64, len(runs))
	turns := make([]float64, len(runs))
	completed := 0
	for i, r := range runs {
		tokens[i] = float64(r.InputTokens)
		turns[i] = float64(r.NumTurns)
		if r.Status == "completed" {
			completed++
		}
	}
	sort.Float64s(tokens)
	sort.Float64s(turns)
	return SkillStats{
		Runs:              len(runs),
		MedianInputTokens: percentile(tokens, 0.5), // duration.go's helper — same interpolated median
		MedianNumTurns:    percentile(turns, 0.5),
		CompletedRate:     float64(completed) / float64(len(runs)),
	}
}

// evaluateStale marks eff.Stale/StaleReason per LEARN-TASKS.md LN-11's
// "протухание" rule. usedRecently reports whether any of the project's last
// StaleRunWindow runs was comparable for this skill (its signatures occurred
// at all) — false means the skill's pattern has stopped coming up. Otherwise,
// a skill with enough post-approval evidence whose median input-token cost
// did not drop is flagged as not having paid for itself. Requires Before.Runs
// > 0 to compare against — with no pre-approval baseline at all there is
// nothing to say "did not decrease" relative to.
func evaluateStale(eff *SkillEffect, usedRecently bool) {
	switch {
	case !usedRecently:
		eff.Stale = true
		eff.StaleReason = StaleReasonUnused
	case eff.After.Runs >= StaleMinRunsAfter && eff.Before.Runs > 0 &&
		eff.After.MedianInputTokens >= eff.Before.MedianInputTokens:
		eff.Stale = true
		eff.StaleReason = StaleReasonNoImprovement
	}
}
