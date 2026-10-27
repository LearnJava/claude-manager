package experience

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
)

// Quality labels (WASTE-TASKS.md WS-11). Objective signs — gates green, how many
// rounds — say whether a task worked, not what went wrong. A task that ended
// badly gets one label from a fixed list plus a reason; a task that ended well
// is never looked at, so it costs nothing. The judge is the cheapest model and
// its tokens are added to the run it judged, so the measuring does not hide
// from the spend it measures.

// How a task ended badly. Only these two are final: a single unfinished run is
// retried by the next session and is not a verdict yet.
const (
	OutcomeGatesRed       = "gates_red"       // still red after the allowed rework rounds
	OutcomeUnfinishedLoop = "unfinished_loop" // ended unfinished run after run until the loop was stopped
)

// LabelInput is a task that ended badly together with the evidence for a judge.
type LabelInput struct {
	Project  string
	TaskPtr  string
	RunID    int64
	Outcome  string
	TaskText string
	Feedback string // red gates' output, or the agent's last words
	Diff     string // changes against the task's starting commit
}

// LabelJudgeFunc judges one task. Wired to analysis.GenerateTaskLabel in app.go;
// a stub in tests.
type LabelJudgeFunc func(ctx context.Context, in analysis.TaskLabelInput) (*analysis.TaskLabelResult, error)

// LabelSkipReason says why a task is not to be judged, "" when it is. A task
// that did not end badly is never judged: the judgement only costs window.
func LabelSkipReason(in LabelInput) string {
	switch {
	case in.Outcome != OutcomeGatesRed && in.Outcome != OutcomeUnfinishedLoop:
		return "the task did not end badly"
	case strings.TrimSpace(in.TaskText) == "" && strings.TrimSpace(in.Feedback) == "":
		return "there is nothing to judge from"
	}
	return ""
}

func outcomeLine(outcome string) string {
	switch outcome {
	case OutcomeGatesRed:
		return "the project's gates stayed red after every allowed rework round"
	case OutcomeUnfinishedLoop:
		return "run after run ended without the agent declaring the task done, until the loop was stopped"
	}
	return outcome
}

// LabelTask judges a task that ended badly and stores the label. Returns
// (nil, nil) without any model call when the task is not one to judge, was
// already labelled, or the judge finds no cause (LabelNone). The judge's tokens
// are charged to the run either way; a judge that fails or answers outside the
// list stores nothing and returns the error.
func LabelTask(ctx context.Context, st *store.Store, in LabelInput, judge LabelJudgeFunc) (*store.TaskLabel, error) {
	if st == nil || judge == nil || LabelSkipReason(in) != "" {
		return nil, nil
	}
	if done, err := st.HasTaskLabelForRun(in.RunID); err != nil || done {
		return nil, err
	}
	res, err := judge(ctx, analysis.TaskLabelInput{
		TaskText: in.TaskText, Outcome: outcomeLine(in.Outcome), Feedback: in.Feedback, Diff: in.Diff,
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("task label: the judge returned nothing")
	}
	if in.RunID != 0 && (res.InputTokens != 0 || res.OutputTokens != 0 || res.CostUSD != 0) {
		if err := st.AddRunUsage(in.RunID, res.InputTokens, res.OutputTokens, res.CostUSD); err != nil {
			return nil, fmt.Errorf("task label: charge the run: %w", err)
		}
	}
	if res.Label == analysis.LabelNone {
		return nil, nil
	}
	l := &store.TaskLabel{
		Project: in.Project, TaskPtr: in.TaskPtr, RunID: in.RunID, Label: res.Label,
		Reason: strings.TrimSpace(res.Reason), Model: res.Model, Tokens: res.InputTokens + res.OutputTokens,
	}
	if err := st.InsertTaskLabel(l); err != nil {
		return nil, err
	}
	return l, nil
}

// LabelCount is how many tasks carry one label.
type LabelCount struct {
	Label string
	Count int
}

// LabelReport is a project's labelled tasks: counts per label (every label of
// the list, zeros included) and the newest entries.
type LabelReport struct {
	Project string
	Total   int
	Tokens  int64 // what all the judgements cost
	Counts  []LabelCount
	Recent  []*store.TaskLabel
}

// recentLabels is how many entries a report lists.
const recentLabels = 50

// BuildLabelReport summarises a project's labels. Read only.
func BuildLabelReport(st *store.Store, project string) (LabelReport, error) {
	rep := LabelReport{Project: project, Counts: []LabelCount{}, Recent: []*store.TaskLabel{}}
	if st == nil {
		return rep, errors.New("no store")
	}
	all, err := st.ListTaskLabels(project, 0)
	if err != nil {
		return rep, err
	}
	byLabel := map[string]int{}
	for _, l := range all {
		byLabel[l.Label]++
		rep.Tokens += l.Tokens
	}
	rep.Total = len(all)
	for _, name := range store.TaskLabelNames {
		rep.Counts = append(rep.Counts, LabelCount{Label: name, Count: byLabel[name]})
	}
	if len(all) > recentLabels {
		all = all[:recentLabels]
	}
	rep.Recent = append(rep.Recent, all...)
	return rep, nil
}
