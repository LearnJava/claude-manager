package experience

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Idle names why no distillation happened this tick ("busy",
	// "trial_in_progress", "gap", "budget", "no_candidate"); "" when one did.
	Idle string `json:"idle,omitempty"`
}

// Changed reports whether the tick changed any skill row.
func (r SkillPilotReport) Changed() bool {
	return len(r.Applied)+len(r.Kept)+len(r.Archived)+len(r.Rejected) > 0
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
	err := p.distillNext(ctx, params, &rep)
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
	spentToday := 0.0
	known := make(map[string]bool)
	taken := make(map[string]bool)
	for i := range skills {
		sk := &skills[i]
		if sk.Status == store.SkillStatusTrial {
			rep.Idle = "trial_in_progress"
			return nil
		}
		known[SigKey(sk.SourceJSON)] = true
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
			if sameDay(sk.CreatedAt, now) {
				spentToday += sk.CostUSD
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
	if params.DailyBudgetUSD > 0 && spentToday >= params.DailyBudgetUSD {
		rep.Idle = "budget"
		return nil
	}

	candidates, err := p.deps.Candidates(st, params.Project)
	if err != nil {
		return err
	}
	threshold := ResolveSkillMinScore(candidates, 0, 0)
	var pick *SkillCandidate
	for i := range candidates {
		c := &candidates[i]
		if c.Kind != KindSkill || c.ContextLossSuspect || c.Score < threshold {
			continue
		}
		if known[SigKey(sigJSON(c.Sig))] {
			continue
		}
		if pick == nil || c.Score > pick.Score {
			pick = c
		}
	}
	if pick == nil {
		rep.Idle = "no_candidate"
		return nil
	}

	in := SkillDistillInputFromCandidate(*pick, params.Gates)
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
		row.Reason = SkillReason{Code: ReasonDistillFailed, Text: err.Error()}.Encode()
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
		row.Reason = SkillReason{Code: ReasonReviewFailed, Text: err.Error()}.Encode()
		return p.insertRejected(row, rep)
	}
	row.CostUSD += review.CostUSD
	if !review.Accept {
		row.Reason = SkillReason{Code: ReasonReviewRejected, Text: review.Reason, DuplicateOf: review.DuplicateOf}.Encode()
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
		return reject(SkillReason{Code: ReasonReviewFailed, Text: err.Error()})
	}
	if review.CostUSD > 0 {
		if err := st.AddSkillCost(sk.ID, review.CostUSD); err != nil {
			return err
		}
	}
	if !review.Accept {
		return reject(SkillReason{Code: ReasonReviewRejected, Text: review.Reason, DuplicateOf: review.DuplicateOf})
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
	for _, s := range c.Samples {
		in.Samples = append(in.Samples, analysis.SkillSample{Command: s.Arg})
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
