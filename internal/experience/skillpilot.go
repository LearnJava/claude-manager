package experience

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
)

// Skill autopilot (LEARN-TASKS.md LN-25/26): distill → review → apply on
// trial → measure → keep or switch off, with no click in between. A human
// cannot judge a skill from its markdown any better than a trial can, so the
// safety comes from reversibility (switching off moves the file to the
// archive, a restore brings it back), measurement (actual loads, before/after
// effect) and a budget — not from an approval step.

const (
	// SkillTrialRuns is how many indexed runs a freshly applied skill gets
	// before the autopilot decides whether to keep it.
	SkillTrialRuns = 10
	// AutoDistillGapRuns is the minimum number of indexed runs between two
	// autopilot distillations — together with "one trial at a time" it
	// bounds spend by project activity, not by wall-clock.
	AutoDistillGapRuns = 5
	// worseTokenFactor: a trial whose after-approval median input tokens
	// exceed the before median by this factor is switched off as harmful.
	worseTokenFactor = 1.2
	// worseCompletedDrop: … or whose completed-run share drops by this much.
	worseCompletedDrop = 0.2
)

// Skill reason codes (SkillReason.Code) — the UI translates each one.
const (
	ReasonAppliedReview  = "applied_review"  // trial: reviewer accepted (Text = its reason)
	ReasonAppliedManual  = "applied_manual"  // applied by a click
	ReasonRestoredManual = "restored_manual" // brought back from the archive by a click
	ReasonKept           = "kept"            // trial passed: Loads in Runs
	ReasonUnusedTrial    = "unused_trial"    // trial: not loaded once in Runs
	ReasonWorse          = "worse"           // trial: runs got worse (Before/After tokens)
	ReasonUnusedLately   = "unused_lately"   // kept skill not loaded in Runs
	ReasonReviewRejected = "review_rejected" // reviewer said no (Text; DuplicateOf)
	ReasonNameTaken      = "name_taken"      // a skill with this name already exists
	ReasonInvalidName    = "invalid_name"    // distiller produced an unusable name
	ReasonDistillFailed  = "distill_failed"  // distillation call failed (Text)
	ReasonReviewFailed   = "review_failed"   // review call failed (Text)
	ReasonArchivedManual = "archived_manual" // switched off by a click
)

// SkillReason is why a skill row is in its current status — JSON-encoded
// into store.Skill.Reason.
type SkillReason struct {
	Code        string  `json:"code"`
	Text        string  `json:"text,omitempty"`
	Runs        int     `json:"runs,omitempty"`
	Loads       int     `json:"loads,omitempty"`
	Before      float64 `json:"before,omitempty"`
	After       float64 `json:"after,omitempty"`
	DuplicateOf string  `json:"duplicate_of,omitempty"`
	// Generic marks a reviewer rejection as "generic advice" (LN-34).
	Generic bool `json:"generic,omitempty"`
	// Attempts counts failed review calls of a draft (ReasonReviewFailed).
	Attempts int `json:"attempts,omitempty"`
}

// MaxDistillAttempts is how many failed distill/review calls one signature
// gets before the autopilot gives up on it.
const MaxDistillAttempts = 3

// maxFailureTextRunes caps the error text stored in a failure SkillReason.
const maxFailureTextRunes = 300

// decodeSkillReason parses store.Skill.Reason; an empty or broken string
// yields the zero value.
func decodeSkillReason(s string) SkillReason {
	var r SkillReason
	if s == "" || json.Unmarshal([]byte(s), &r) != nil {
		return SkillReason{}
	}
	return r
}

// failureText is err's message capped to maxFailureTextRunes runes.
func failureText(err error) string {
	return truncateRunes(err.Error(), maxFailureTextRunes)
}

// Encode returns r as the JSON stored in store.Skill.Reason.
func (r SkillReason) Encode() string {
	b, _ := json.Marshal(r)
	return string(b)
}

// SkillPilotDeps are the autopilot's paid calls, injectable for tests.
type SkillPilotDeps struct {
	Store   *store.Store
	Distill func(ctx context.Context, projectPath string, in analysis.SkillDistillInput) (*analysis.SkillDraft, error)
	Review  func(ctx context.Context, projectPath string, in analysis.SkillReviewInput) (*analysis.SkillReview, error)
	Now     func() time.Time
	// Candidates mines the project's skill candidates; nil uses
	// MineProjectCandidates.
	Candidates func(st *store.Store, project string) ([]SkillCandidate, error)
	// DraftRule drafts a one-line rule for a recurring error (LN-40); nil
	// disables drafting new rules (ready drafts are still applied).
	DraftRule func(ctx context.Context, projectPath string, in analysis.FailureRuleInput) (*analysis.FailureRuleDraft, error)
}

// SkillPilotParams is one project's autopilot input.
type SkillPilotParams struct {
	Project     string
	ProjectPath string
	Gates       []string
	// DailyBudgetUSD caps what autopilot distillations+reviews may spend per
	// day in this project; 0 = no cap (the run-gap and one-trial-at-a-time
	// rules still bound it).
	DailyBudgetUSD float64
}

// SkillPilotReport says what one Tick did.
type SkillPilotReport struct {
	Applied  []string `json:"applied,omitempty"`
	Kept     []string `json:"kept,omitempty"`
	Archived []string `json:"archived,omitempty"`
	Rejected []string `json:"rejected,omitempty"`
	// Failure rules (LN-40): rule texts, at most 80 runes each.
	RulesApplied  []string `json:"rules_applied,omitempty"`
	RulesKept     []string `json:"rules_kept,omitempty"`
	RulesReverted []string `json:"rules_reverted,omitempty"`
	// Idle names why no distillation happened this tick ("busy",
	// "trial_in_progress", "gap", "budget", "no_candidate"); "" when one did.
	Idle string `json:"idle,omitempty"`
}

// Changed reports whether the tick changed any skill row.
func (r SkillPilotReport) Changed() bool {
	return len(r.Applied)+len(r.Kept)+len(r.Archived)+len(r.Rejected)+
		len(r.RulesApplied)+len(r.RulesKept)+len(r.RulesReverted) > 0
}

// SkillPilot runs the autopilot; one instance serves every project.
type SkillPilot struct {
	deps SkillPilotDeps
	mu   sync.Mutex
	busy map[string]bool
}

// NewSkillPilot returns an autopilot over deps.
func NewSkillPilot(deps SkillPilotDeps) *SkillPilot {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Candidates == nil {
		deps.Candidates = MineProjectCandidates
	}
	return &SkillPilot{deps: deps, busy: make(map[string]bool)}
}

// Tick runs one autopilot step for a project, normally after each finished
// and indexed run: first decide on skills whose trial is over (and switch
// off kept skills nobody loads any more), then — if nothing is on trial —
// distill, review and apply at most one new candidate. A tick already in
// flight for the same project makes this one a no-op.
func (p *SkillPilot) Tick(ctx context.Context, params SkillPilotParams) (SkillPilotReport, error) {
	var rep SkillPilotReport
	p.mu.Lock()
	if p.busy[params.Project] {
		p.mu.Unlock()
		rep.Idle = "busy"
		return rep, nil
	}
	p.busy[params.Project] = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.busy, params.Project)
		p.mu.Unlock()
	}()

	if err := p.evaluate(params, &rep); err != nil {
		return rep, err
	}
	// A failing rules step must not starve skills: run distillNext anyway
	// and return the first error.
	rulesErr := p.rulesStep(ctx, params, &rep)
	err := p.distillNext(ctx, params, &rep)
	if rulesErr != nil {
		return rep, rulesErr
	}
	return rep, err
}

// evaluate decides on finished trials and on kept skills gone unused.
func (p *SkillPilot) evaluate(params SkillPilotParams, rep *SkillPilotReport) error {
	st := p.deps.Store
	skills, err := st.ListSkills(params.Project)
	if err != nil {
		return err
	}
	usage, err := BuildSkillUsage(st, params.Project)
	if err != nil {
		return err
	}
	var effects map[int64]SkillEffect
	now := p.deps.Now()

	for _, sk := range skills {
		u := usage[sk.ID]
		switch sk.Status {
		case store.SkillStatusTrial:
			if u.RunsSinceApplied < SkillTrialRuns {
				continue
			}
			if u.RunsWithLoad == 0 {
				r := SkillReason{Code: ReasonUnusedTrial, Runs: u.RunsSinceApplied}
				if err := p.archive(params.ProjectPath, sk, r, now); err != nil {
					return err
				}
				rep.Archived = append(rep.Archived, sk.Name)
				continue
			}
			if effects == nil {
				if effects, err = skillEffectsByID(st, params.Project); err != nil {
					return err
				}
			}
			if eff, ok := effects[sk.ID]; ok && trialMadeThingsWorse(eff) {
				r := SkillReason{Code: ReasonWorse, Runs: u.RunsSinceApplied, Loads: u.RunsWithLoad,
					Before: eff.Before.MedianInputTokens, After: eff.After.MedianInputTokens}
				if err := p.archive(params.ProjectPath, sk, r, now); err != nil {
					return err
				}
				rep.Archived = append(rep.Archived, sk.Name)
				continue
			}
			r := SkillReason{Code: ReasonKept, Runs: u.RunsSinceApplied, Loads: u.RunsWithLoad}
			if err := st.UpdateSkillStatus(sk.ID, store.SkillStatusApproved, r.Encode(), now); err != nil {
				return err
			}
			rep.Kept = append(rep.Kept, sk.Name)
		case store.SkillStatusApproved:
			if sk.ApprovedAt == nil || u.RunsSinceLastLoad < StaleRunWindow {
				continue
			}
			r := SkillReason{Code: ReasonUnusedLately, Runs: u.RunsSinceLastLoad}
			if err := p.archive(params.ProjectPath, sk, r, now); err != nil {
				return err
			}
			rep.Archived = append(rep.Archived, sk.Name)
		}
	}
	return nil
}

// trialMadeThingsWorse applies the "switch off as harmful" rule to an LN-11
// effect — only with enough data on both sides to say anything.
func trialMadeThingsWorse(eff SkillEffect) bool {
	if eff.InsufficientData || eff.Before.MedianInputTokens <= 0 {
		return false
	}
	return eff.After.MedianInputTokens > eff.Before.MedianInputTokens*worseTokenFactor ||
		eff.Before.CompletedRate-eff.After.CompletedRate >= worseCompletedDrop
}

func skillEffectsByID(st *store.Store, project string) (map[int64]SkillEffect, error) {
	report, err := BuildSkillQualityReport(st, project)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]SkillEffect, len(report))
	for _, e := range report {
		out[e.SkillID] = e
	}
	return out, nil
}

// archive switches a skill off: its folder leaves .claude/skills, the row
// records why.
func (p *SkillPilot) archive(projectPath string, sk store.Skill, r SkillReason, now time.Time) error {
	if err := ArchiveSkillFile(projectPath, sk.Name, sk.ID); err != nil {
		return err
	}
	return p.deps.Store.UpdateSkillStatus(sk.ID, store.SkillStatusArchived, r.Encode(), now)
}

// autopilotReason reports whether the autopilot may take a candidate: only a
// skill backed by a known failure or a real multi-step sequence. A lone step
// (single_step) has the biggest Score yet no procedure in it, so it stays
// available to manual distillation only (LN-33).
func autopilotReason(c SkillCandidate) bool {
	return c.Kind == KindSkill && (c.KindReason == reasonKnownFailure || c.KindReason == reasonMultiStep)
}

// betterPick orders autopilot candidates: known_failure first, then Score.
func betterPick(c, cur *SkillCandidate) bool {
	cf, curf := c.KindReason == reasonKnownFailure, cur.KindReason == reasonKnownFailure
	if cf != curf {
		return cf
	}
	return c.Score > cur.Score
}

// distillNext distills, reviews and applies at most one new candidate.
func (p *SkillPilot) distillNext(ctx context.Context, params SkillPilotParams, rep *SkillPilotReport) error {
	st := p.deps.Store
	skills, err := st.ListSkills(params.Project)
	if err != nil {
		return err
	}
	now := p.deps.Now()

	// One experiment at a time: a second skill applied mid-trial would
	// make both trials' before/after numbers meaningless.
	var lastAuto, pending *store.Skill
	known := make(map[string]bool)
	failures := make(map[string]int)
	genericSteps := make(map[string]bool)
	taken := make(map[string]bool)
	for i := range skills {
		sk := &skills[i]
		if sk.Status == store.SkillStatusTrial {
			rep.Idle = "trial_in_progress"
			return nil
		}
		if sk.Status == store.SkillStatusRejected && isCallFailure(sk.Reason) {
			// A failed call evaluated nothing: retry until MaxDistillAttempts.
			failures[SigKey(sk.SourceJSON)]++
		} else {
			known[SigKey(sk.SourceJSON)] = true
		}
		if sk.Status == store.SkillStatusRejected {
			if r := decodeSkillReason(sk.Reason); r.Code == ReasonReviewRejected && r.Generic {
				var steps []string
				if json.Unmarshal([]byte(sk.SourceJSON), &steps) == nil {
					for _, s := range steps {
						genericSteps[s] = true
					}
				}
			}
		}
		if sk.Status == store.SkillStatusApproved {
			taken[sk.Name] = true
		}
		if sk.Status == store.SkillStatusDraft && pending == nil {
			pending = sk // newest first: the most recent draft
		}
		if sk.Origin == store.SkillOriginAuto {
			if lastAuto == nil || sk.CreatedAt.After(lastAuto.CreatedAt) {
				lastAuto = sk
			}
		}
	}
	// A draft someone already paid to distill is reviewed before anything
	// new is distilled — no gap: a review is cheap, and applying is still
	// gated by one trial at a time.
	if pending != nil {
		return p.reviewDraft(ctx, params, *pending, taken, rep)
	}
	if lastAuto != nil {
		runs, err := indexedRuns(st, params.Project)
		if err != nil {
			return err
		}
		since := 0
		for _, r := range runs {
			if r.StartedAt.After(lastAuto.CreatedAt) {
				since++
			}
		}
		if since < AutoDistillGapRuns {
			rep.Idle = "gap"
			return nil
		}
	}
	spentToday, err := autoSpentToday(st, params.Project, now)
	if err != nil {
		return err
	}
	if params.DailyBudgetUSD > 0 && spentToday >= params.DailyBudgetUSD {
		rep.Idle = "budget"
		return nil
	}

	candidates, err := p.deps.Candidates(st, params.Project)
	if err != nil {
		return err
	}
	for k, n := range failures {
		if n >= MaxDistillAttempts {
			known[k] = true
		}
	}
	// The threshold is taken over the candidates still in play, so a
	// reviewed top 3% does not push every remaining one below it.
	var open []SkillCandidate
	for _, c := range candidates {
		if autopilotReason(c) && !c.ContextLossSuspect && !known[SigKey(sigJSON(c.Sig))] && !allGeneric(c.Sig, genericSteps) {
			open = append(open, c)
		}
	}
	threshold := ResolveSkillMinScore(open, 0, 0)
	var pick *SkillCandidate
	for i := range open {
		c := &open[i]
		if c.Score < threshold {
			continue
		}
		if pick == nil || betterPick(c, pick) {
			pick = c
		}
	}
	if pick == nil {
		rep.Idle = "no_candidate"
		return nil
	}

	in := SkillDistillInputFromCandidate(*pick, params.Gates)
	in.RejectedBefore = RejectedBefore(st, params.Project, 5)
	row := &store.Skill{
		Project:    params.Project,
		SourceJSON: sigJSON(pick.Sig),
		CreatedAt:  now,
		Origin:     store.SkillOriginAuto,
		DraftJSON:  "{}",
		Status:     store.SkillStatusRejected,
	}

	draft, err := p.deps.Distill(ctx, params.ProjectPath, in)
	if err != nil {
		row.Name = fallbackSkillName(pick.Sig)
		row.Reason = SkillReason{Code: ReasonDistillFailed, Text: failureText(err)}.Encode()
		return p.insertRejected(row, rep)
	}
	draftJSON, _ := json.Marshal(draft)
	row.Name = draft.Name
	row.DraftJSON = string(draftJSON)
	row.MD = analysis.RenderSkillMarkdown(*draft)
	row.CostUSD = draft.CostUSD

	if !ValidSkillName(draft.Name) {
		row.Name = fallbackSkillName(pick.Sig)
		row.Reason = SkillReason{Code: ReasonInvalidName, Text: draft.Name}.Encode()
		return p.insertRejected(row, rep)
	}
	existing := ListProjectSkills(params.ProjectPath)
	for _, e := range existing {
		taken[e.Name] = true
	}
	if taken[draft.Name] {
		row.Reason = SkillReason{Code: ReasonNameTaken, DuplicateOf: draft.Name}.Encode()
		return p.insertRejected(row, rep)
	}

	reviewIn := analysis.SkillReviewInput{Sig: pick.Sig, RunShare: pick.RunShare, DraftMD: row.MD}
	for _, e := range existing {
		reviewIn.Existing = append(reviewIn.Existing, analysis.ExistingSkillSummary{Name: e.Name, Description: e.Description})
	}
	review, err := p.deps.Review(ctx, params.ProjectPath, reviewIn)
	if err != nil {
		row.Reason = SkillReason{Code: ReasonReviewFailed, Text: failureText(err)}.Encode()
		return p.insertRejected(row, rep)
	}
	row.CostUSD += review.CostUSD
	if !review.Accept {
		row.Reason = SkillReason{Code: ReasonReviewRejected, Text: review.Reason, DuplicateOf: review.DuplicateOf, Generic: review.Generic}.Encode()
		return p.insertRejected(row, rep)
	}

	if _, err := WriteSkillFile(params.ProjectPath, row.Name, row.MD, false); err != nil {
		if err == ErrSkillFileExists {
			row.Reason = SkillReason{Code: ReasonNameTaken, DuplicateOf: row.Name}.Encode()
			return p.insertRejected(row, rep)
		}
		return err
	}
	row.Status = store.SkillStatusTrial
	row.ApprovedAt = &now
	row.Reason = SkillReason{Code: ReasonAppliedReview, Text: review.Reason}.Encode()
	if err := st.InsertSkill(row); err != nil {
		return err
	}
	rep.Applied = append(rep.Applied, row.Name)
	return nil
}

// reviewDraft runs the reviewer over an existing (manually distilled) draft
// and applies it on trial or rejects it — the same bar a fresh autopilot
// draft has to clear.
func (p *SkillPilot) reviewDraft(ctx context.Context, params SkillPilotParams, sk store.Skill, taken map[string]bool, rep *SkillPilotReport) error {
	st := p.deps.Store
	now := p.deps.Now()
	reject := func(r SkillReason) error {
		if err := st.UpdateSkillStatus(sk.ID, store.SkillStatusRejected, r.Encode(), now); err != nil {
			return err
		}
		rep.Rejected = append(rep.Rejected, sk.Name)
		return nil
	}
	if !ValidSkillName(sk.Name) {
		return reject(SkillReason{Code: ReasonInvalidName, Text: sk.Name})
	}
	existing := ListProjectSkills(params.ProjectPath)
	for _, e := range existing {
		taken[e.Name] = true
	}
	if taken[sk.Name] {
		return reject(SkillReason{Code: ReasonNameTaken, DuplicateOf: sk.Name})
	}
	var sig []string
	_ = json.Unmarshal([]byte(sk.SourceJSON), &sig)
	in := analysis.SkillReviewInput{Sig: sig, DraftMD: sk.MD}
	for _, e := range existing {
		in.Existing = append(in.Existing, analysis.ExistingSkillSummary{Name: e.Name, Description: e.Description})
	}
	review, err := p.deps.Review(ctx, params.ProjectPath, in)
	if err != nil {
		// The draft is already paid for: keep it for another try.
		r := SkillReason{Code: ReasonReviewFailed, Text: failureText(err),
			Attempts: decodeSkillReason(sk.Reason).Attempts + 1}
		if r.Attempts >= MaxDistillAttempts {
			return reject(r)
		}
		return st.UpdateSkillStatus(sk.ID, store.SkillStatusDraft, r.Encode(), now)
	}
	if review.CostUSD > 0 {
		if err := st.AddSkillCost(sk.ID, review.CostUSD); err != nil {
			return err
		}
	}
	if !review.Accept {
		return reject(SkillReason{Code: ReasonReviewRejected, Text: review.Reason, DuplicateOf: review.DuplicateOf, Generic: review.Generic})
	}
	if _, err := WriteSkillFile(params.ProjectPath, sk.Name, sk.MD, false); err != nil {
		if err == ErrSkillFileExists {
			return reject(SkillReason{Code: ReasonNameTaken, DuplicateOf: sk.Name})
		}
		return err
	}
	r := SkillReason{Code: ReasonAppliedReview, Text: review.Reason}
	if err := st.UpdateSkillApproved(sk.ID, sk.MD, store.SkillStatusTrial, r.Encode(), now); err != nil {
		return err
	}
	rep.Applied = append(rep.Applied, sk.Name)
	return nil
}

// isCallFailure reports whether a rejected row's reason is a failed paid call
// rather than a verdict.
func isCallFailure(reason string) bool {
	c := decodeSkillReason(reason).Code
	return c == ReasonDistillFailed || c == ReasonReviewFailed
}

func (p *SkillPilot) insertRejected(row *store.Skill, rep *SkillPilotReport) error {
	row.Status = store.SkillStatusRejected
	if err := p.deps.Store.InsertSkill(row); err != nil {
		return err
	}
	rep.Rejected = append(rep.Rejected, row.Name)
	return nil
}

// fallbackSkillName names a rejected row whose draft has no usable name.
func fallbackSkillName(sig []string) string {
	if len(sig) == 0 {
		return "unnamed"
	}
	return fmt.Sprintf("candidate-%d-steps", len(sig))
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Local().Date()
	by, bm, bd := b.Local().Date()
	return ay == by && am == bm && ad == bd
}

// sigJSON encodes a signature sequence the way store.Skill.SourceJSON holds
// it (Go's json.Marshal, which escapes "<" and ">").
func sigJSON(sig []string) string {
	b, _ := json.Marshal(sig)
	return string(b)
}

// allGeneric reports whether every step of sig was part of a signature the
// reviewer rejected as generic (LN-34). An empty sig is never generic.
func allGeneric(sig []string, generic map[string]bool) bool {
	if len(sig) == 0 {
		return false
	}
	for _, s := range sig {
		if !generic[s] {
			return false
		}
	}
	return true
}

// maxRejectedReasonRunes caps one reason in RejectedBefore.
const maxRejectedReasonRunes = 200

// RejectedBefore returns up to n of the project's latest reviewer rejections
// as "<name>: <reason>", newest first (LN-34), for the distiller prompt.
func RejectedBefore(st *store.Store, project string, n int) []string {
	if st == nil || n <= 0 {
		return nil
	}
	skills, err := st.ListSkills(project)
	if err != nil {
		return nil
	}
	var out []string
	for _, sk := range skills {
		if len(out) >= n {
			break
		}
		if sk.Status != store.SkillStatusRejected {
			continue
		}
		r := decodeSkillReason(sk.Reason)
		if r.Code != ReasonReviewRejected {
			continue
		}
		text := []rune(r.Text)
		if len(text) > maxRejectedReasonRunes {
			text = text[:maxRejectedReasonRunes]
		}
		out = append(out, sk.Name+": "+string(text))
	}
	return out
}

// SigKey normalizes a SourceJSON value for equality checks: decode and
// re-encode, so two encoders' escaping choices compare equal. Invalid JSON
// is returned as-is.
func SigKey(sourceJSON string) string {
	var sig []string
	if err := json.Unmarshal([]byte(sourceJSON), &sig); err != nil {
		return sourceJSON
	}
	return sigJSON(sig)
}

// SkillDistillInputFromCandidate translates one LN-08 SkillCandidate (plus
// the project's gates) into analysis.SkillDistillInput, taking one
// representative example per related failure cluster — enough context for
// the distillation prompt without re-exporting FailureCluster's full shape.
// Shared by the manual Distill button (app.go) and the autopilot.
func SkillDistillInputFromCandidate(c SkillCandidate, gates []string) analysis.SkillDistillInput {
	in := analysis.SkillDistillInput{
		Sig:   append([]string(nil), c.Sig...),
		Gates: append([]string(nil), gates...),
	}
	if len(c.Occurrences) > 0 {
		for _, occ := range c.Occurrences {
			lines := make([]string, 0, len(occ))
			for _, r := range occ {
				if r.Tool == "Bash" {
					lines = append(lines, r.Arg)
				} else {
					lines = append(lines, r.Tool+" "+r.Arg)
				}
			}
			in.Samples = append(in.Samples, analysis.SkillSample{Command: strings.Join(lines, "\n")})
		}
	} else {
		for _, s := range c.Samples {
			in.Samples = append(in.Samples, analysis.SkillSample{Command: s.Arg})
		}
	}
	for _, f := range c.RelatedFailures {
		if len(f.Examples) == 0 {
			continue
		}
		ex := f.Examples[0]
		in.RelatedFailures = append(in.RelatedFailures, analysis.SkillFailureSummary{
			ErrorKey:  f.ErrorKey,
			FailedArg: ex.FailedArg,
			FixedArg:  ex.FixedArg,
		})
	}
	return in
}
