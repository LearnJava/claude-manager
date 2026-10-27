package main

import (
	"fmt"

	"claude-manager/internal/experience"
)

// GetTaskLabels reports the quality labels of a project's tasks that ended
// badly: how many carry each cause (tests fitted to the code, protocol broken,
// garbage left, wrong task done), what the judging cost, and the newest labels
// with their reasons (WASTE-TASKS.md WS-11). Read only; empty until
// [optimization] quality_labels is on and a task ends badly.
func (a *App) GetTaskLabels(project string) (experience.LabelReport, error) {
	if a.store == nil {
		return experience.LabelReport{}, fmt.Errorf("no store")
	}
	return experience.BuildLabelReport(a.store, project)
}
