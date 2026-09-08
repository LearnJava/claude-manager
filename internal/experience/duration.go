package experience

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"claude-manager/internal/store"
)

// MinDurationSamples is the smallest sample size DurationProfile will report
// a signature for — a median of 2 calls is noise, not a profile
// (LEARN-TASKS.md LN-18: "только сигнатуры с n ≥ 10").
const MinDurationSamples = 10

// durationWindowDays bounds ActionDurations the same way TopSignatures bounds
// its own window — a "how long does this project's build take" profile
// should reflect current behavior, not a command that was slow a year ago
// and has since been fixed.
const durationWindowDays = 90

// SignatureDuration aggregates action_signatures.dur_sec for one (project,
// sig) pair: how long a normalized command actually takes in this project,
// the one thing a fresh session cannot know without hitting a timeout itself
// (LEARN-TASKS.md LN-18).
type SignatureDuration struct {
	Sig       string
	Tool      string
	Count     int // calls with a known duration (dur_sec > 0); n < MinDurationSamples is dropped
	MedianSec float64
	P90Sec    float64
	MaxSec    float64
	TotalSec  float64 // sum of all sampled durations — "where did the run's time go"
	FailRate  float64 // share of the sampled calls that ended in error
}

// DurationProfile aggregates one project's action_signatures durations into
// per-signature stats, most time-consuming (by median) first. Only rows with
// a known duration count (dur_sec > 0, see store.ActionDurations); a
// signature with fewer than MinDurationSamples such rows is dropped rather
// than reported off an unreliable sample.
func DurationProfile(st *store.Store, project string) ([]SignatureDuration, error) {
	rows, err := st.ActionDurations(project, durationWindowDays)
	if err != nil {
		return nil, err
	}

	type acc struct {
		tool string
		durs []float64
		errs int
	}
	groups := make(map[string]*acc)
	var order []string
	for _, r := range rows {
		g, ok := groups[r.Sig]
		if !ok {
			g = &acc{tool: r.Tool}
			groups[r.Sig] = g
			order = append(order, r.Sig)
		}
		g.durs = append(g.durs, float64(r.DurSec))
		if r.IsError {
			g.errs++
		}
	}

	var out []SignatureDuration
	for _, sig := range order {
		g := groups[sig]
		if len(g.durs) < MinDurationSamples {
			continue
		}
		sort.Float64s(g.durs)
		var total float64
		for _, d := range g.durs {
			total += d
		}
		out = append(out, SignatureDuration{
			Sig:       sig,
			Tool:      g.tool,
			Count:     len(g.durs),
			MedianSec: percentile(g.durs, 0.5),
			P90Sec:    percentile(g.durs, 0.9),
			MaxSec:    g.durs[len(g.durs)-1],
			TotalSec:  total,
			FailRate:  float64(g.errs) / float64(len(g.durs)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MedianSec > out[j].MedianSec })
	return out, nil
}

// percentile linearly interpolates the p-th percentile (0..1) of an
// already-sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := p * float64(len(sorted)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}

// stepDurSec computes the whole-second gap between a step's tool_use and its
// result, rounded to the nearest second. Zero means unknown — either the
// tool_use timestamp, the result timestamp, or both, are unavailable (no
// result arrived within the ingested window) — never "instant"; callers must
// not treat 0 as a real sample (LEARN-TASKS.md LN-18: "вызов без результата
// даёт dur_sec = 0 и не искажает медиану").
func stepDurSec(step Step) int64 {
	if step.Time.IsZero() || step.ResultTime.IsZero() {
		return 0
	}
	d := step.ResultTime.Sub(step.Time)
	if d <= 0 {
		return 0
	}
	return int64(d.Round(time.Second) / time.Second)
}

// durationThresholdSec is the minimum median a signature needs to appear in
// the primer's timing section — a 5-second command needs no timeout warning
// (LEARN-TASKS.md LN-18).
const durationThresholdSec = 60.0

// maxDurationLines caps the timing section (LEARN-TASKS.md LN-18: "не более
// 5 строк") — a context primer that lists every slow command in the project
// defeats its own purpose.
const maxDurationLines = 5

// durationSection renders BuildPrimer's timing block (LEARN-TASKS.md LN-18):
// one line per slow command naming roughly how long it takes, plus — if
// present — a dedicated line for `sleep`, which gets special wording because
// it is not a slow command but a dead-end anti-pattern in this session model
// (see "Background-Task Warning" in CLAUDE.md: a session's stdin closes at
// turn end, so there is nobody left to wait out the sleep for). Returns ""
// when nothing in profile clears the threshold.
func durationSection(profile []SignatureDuration) string {
	var sleepLine string
	var lines []string
	for _, p := range profile {
		if p.MedianSec < durationThresholdSec {
			continue
		}
		if isSleepSignature(p.Sig) {
			if sleepLine == "" {
				sleepLine = fmt.Sprintf(
					"`sleep` used %d times, ~%s total — that's dead time: this manager closes stdin at turn end, so a sleep-then-check pattern never gets to finish (see CLAUDE.md Background-Task Warning)",
					p.Count, formatDurationSec(p.TotalSec))
			}
			continue
		}
		lines = append(lines, fmt.Sprintf("`%s` takes ~%s in this project — set a generous timeout or split the work",
			commandLabel(p.Sig), formatDurationSec(p.MedianSec)))
	}

	if sleepLine != "" {
		lines = append(lines, sleepLine)
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > maxDurationLines {
		lines = lines[:maxDurationLines]
	}
	return "Command timing (this project):\n" + strings.Join(lines, "\n")
}

// isSleepSignature reports whether sig is Signature's normalized form of a
// bare `sleep` call ("Bash:sleep <ARG>").
func isSleepSignature(sig string) bool {
	return strings.HasPrefix(sig, "Bash:sleep ") || sig == "Bash:sleep"
}

// commandLabel strips the "Bash:" signature prefix for display — the primer
// reads better as "`git worktree add -b <ARG>` takes ~2min" than
// "`Bash:git worktree add -b <ARG>` takes ~2min". Non-Bash signatures (a slow
// Read/Grep, say) are shown as-is; they already carry their tool name.
func commandLabel(sig string) string {
	if s, ok := strings.CutPrefix(sig, "Bash:"); ok {
		return s
	}
	return sig
}

// formatDurationSec renders a second count as the coarsest unit that keeps it
// readable — a primer line is for a quick "how much slack do I need", not a
// precise measurement.
func formatDurationSec(sec float64) string {
	d := time.Duration(sec) * time.Second
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%dmin", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
