package session

import (
	"context"
	"time"

	"claude-manager/internal/logger"
)

// QualityLabelInfo is a task that ended badly for good together with what the
// manager knows about the run that ended it.
type QualityLabelInfo struct {
	Project     string
	ProjectPath string
	RunID       int64
	// Model is the model the task ran on; the judge's model is the manager's choice.
	Model string
	LabelRecord
}

// QualityLabelFunc judges one task and stores the label. Wired from app.go to
// internal/experience (the usual import-cycle reason, see ActionIndexFunc).
// The model [optimization] quality_label_model names comes as judgeModel.
type QualityLabelFunc func(ctx context.Context, info QualityLabelInfo, judgeModel string) error

// qualityLabelTimeout bounds one judging call.
const qualityLabelTimeout = 3 * time.Minute

// SetQualityLabels wires the labeller of WASTE-TASKS.md WS-11. Nothing is
// captured or judged unless [optimization] quality_labels is on.
func (m *SessionManager) SetQualityLabels(fn QualityLabelFunc) {
	m.mu.Lock()
	m.labelFn = fn
	m.mu.Unlock()
}

// qualityLabelInfo snapshots the ending run for a bad task; it must run before
// finishRun closes the run. nil when nothing is to be judged. When the session
// had no gate output the agent's last words stand in as the evidence.
func (m *SessionManager) qualityLabelInfo(ms *managedSession, rec *LabelRecord) *QualityLabelInfo {
	if rec == nil {
		return nil
	}
	m.mu.Lock()
	fn := m.labelFn
	on := m.cfg != nil && m.cfg.Optimization.QualityLabels
	m.mu.Unlock()
	if fn == nil || !on {
		return nil
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	info := &QualityLabelInfo{
		Project: ms.project, ProjectPath: ms.session.ProjectPath, RunID: ms.runID,
		Model: ms.runModel, LabelRecord: *rec,
	}
	if info.Feedback == "" {
		info.Feedback = ms.lastResultText
	}
	return info
}

// judgeQualityLabel runs the judge in the background — it takes a model call —
// once the run is stored, so the run row exists when its spend is charged.
// A failure is logged, never fatal.
func (m *SessionManager) judgeQualityLabel(info *QualityLabelInfo) {
	if info == nil {
		return
	}
	m.mu.Lock()
	fn := m.labelFn
	model := ""
	if m.cfg != nil {
		model = m.cfg.Optimization.QualityLabelModel
	}
	m.mu.Unlock()
	if fn == nil {
		return
	}
	go func() {
		defer logger.Recover("manager.quality_label", "project", info.Project)
		ctx, cancel := context.WithTimeout(context.Background(), qualityLabelTimeout)
		defer cancel()
		if err := fn(ctx, *info, model); err != nil {
			logger.L.Error("session.quality_label_failed", "project", info.Project, "task", info.TaskPtr, "error", err)
		}
	}()
}
