package worker

import "sort"

// ModelQuality is the per-worker aggregate for the comparative quality report
// (MIXED-TASKS.md MP-08): how many rounds a model needs to go green, how many
// of its patches apply cleanly, and its defects broken down by type. Built
// from persisted MixedTask state — the same records the rounds timeline shows.
type ModelQuality struct {
	Worker           string  `json:"worker"`
	TasksTotal       int     `json:"tasks_total"`
	TasksDone        int     `json:"tasks_done"`
	TasksNeedsHuman  int     `json:"tasks_needs_human"`
	TasksRunning     int     `json:"tasks_running"`
	AvgRoundsToGreen float64 `json:"avg_rounds_to_green"` // mean len(Rounds) over done tasks; 0 when none are done
	PatchesApplied   int     `json:"patches_applied"`
	PatchesRejected  int     `json:"patches_rejected"`
	CleanPatchRate   float64 `json:"clean_patch_rate"` // applied / (applied + rejected); 0 when no patches
	// Defects by type (one count per round where the defect occurred).
	ParseErrors  int `json:"parse_errors"`
	GateFailures int `json:"gate_failures"`
}

// BuildQualityReport aggregates tasks into one ModelQuality per worker,
// sorted by worker name for deterministic display. Nil tasks are skipped.
func BuildQualityReport(tasks []*MixedTask) []ModelQuality {
	byWorker := make(map[string]*ModelQuality)
	greenRounds := make(map[string]int) // worker -> sum of len(Rounds) over done tasks

	for _, t := range tasks {
		if t == nil {
			continue
		}
		q := byWorker[t.WorkerName]
		if q == nil {
			q = &ModelQuality{Worker: t.WorkerName}
			byWorker[t.WorkerName] = q
		}
		q.TasksTotal++
		switch t.Status {
		case TaskStatusDone:
			q.TasksDone++
			greenRounds[t.WorkerName] += len(t.Rounds)
		case TaskStatusNeedsHuman:
			q.TasksNeedsHuman++
		case TaskStatusRunning:
			q.TasksRunning++
		}
		for _, r := range t.Rounds {
			q.PatchesApplied += len(r.Applied)
			q.PatchesRejected += len(r.Rejected)
			if r.ParseError != "" {
				q.ParseErrors++
			}
			// A gate failure is a round whose patches all applied but gates ran red.
			if r.ParseError == "" && len(r.Rejected) == 0 && len(r.Gates.Commands) > 0 && !r.Gates.Passed {
				q.GateFailures++
			}
		}
	}

	out := make([]ModelQuality, 0, len(byWorker))
	for name, q := range byWorker {
		if q.TasksDone > 0 {
			q.AvgRoundsToGreen = float64(greenRounds[name]) / float64(q.TasksDone)
		}
		if total := q.PatchesApplied + q.PatchesRejected; total > 0 {
			q.CleanPatchRate = float64(q.PatchesApplied) / float64(total)
		}
		out = append(out, *q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Worker < out[j].Worker })
	return out
}
