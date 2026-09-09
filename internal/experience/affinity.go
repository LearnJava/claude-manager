package experience

import (
	"strings"

	"claude-manager/internal/store"
)

// SessionAffinityInput is one session's launch-ordering input: the model it
// will launch with and the files its last completed run touched (Read/Edit/
// Write args, from action_signatures — see LastRunFiles).
type SessionAffinityInput struct {
	ID    string
	Model string
	Files []string
}

// OrderByCacheAffinity returns session IDs in cache-friendly launch order
// (LEARN-TASKS.md LN-14): sessions are grouped by launch model first — a
// model switch resets the shared system-prompt cache (see CLAUDE.md "Token
// Optimization": `--exclude-dynamic-system-prompt-sections` only helps
// across starts of the *same* model) — and, within a model group, ordered to
// maximize the file overlap between consecutive starts, on the theory that
// two sessions about to re-read/edit the same files benefit more from
// starting back to back than staggered apart by an unrelated session.
//
// The within-group order is a greedy chain: it starts at the group's first
// session (in input order) and repeatedly appends whichever remaining
// session shares the most files with the one just picked, ties broken by
// input order. Groups themselves are emitted in order of each model's first
// appearance in sessions.
//
// A session with no launch history has an empty Files, which has zero
// overlap with everything; the greedy pick then always ties and falls back
// to input order, so a project with no run history yet (LEARN-TASKS.md
// invariant 6) reproduces the caller's original per-model order
// byte-for-byte instead of some hard-to-predict reshuffle.
func OrderByCacheAffinity(sessions []SessionAffinityInput) []string {
	if len(sessions) == 0 {
		return nil
	}

	// Group indices by model, preserving first-appearance order of both the
	// groups and the sessions inside each group.
	var modelOrder []string
	groups := make(map[string][]int)
	for i, s := range sessions {
		if _, ok := groups[s.Model]; !ok {
			modelOrder = append(modelOrder, s.Model)
		}
		groups[s.Model] = append(groups[s.Model], i)
	}

	var order []string
	for _, model := range modelOrder {
		order = append(order, chainByFileOverlap(sessions, groups[model])...)
	}
	return order
}

// chainByFileOverlap greedily orders the sessions at the given indices (all
// sharing one model) to maximize file overlap between consecutive picks.
// idxs supplies both the starting pick and the tie-break order, so a group
// with no file overlap at all (no history) comes out exactly as given.
func chainByFileOverlap(sessions []SessionAffinityInput, idxs []int) []string {
	remaining := append([]int(nil), idxs...)
	current := remaining[0]
	remaining = remaining[1:]
	order := []string{sessions[current].ID}

	for len(remaining) > 0 {
		bestPos, bestScore := 0, -1
		for pos, idx := range remaining {
			if score := fileOverlap(sessions[current].Files, sessions[idx].Files); score > bestScore {
				bestScore, bestPos = score, pos
			}
		}
		current = remaining[bestPos]
		order = append(order, sessions[current].ID)
		remaining = append(remaining[:bestPos], remaining[bestPos+1:]...)
	}
	return order
}

// fileOverlap counts how many files two sessions' last runs both touched.
func fileOverlap(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, f := range a {
		set[f] = true
	}
	n := 0
	for _, f := range b {
		if set[f] {
			n++
		}
	}
	return n
}

// LastRunFiles returns the files a session's most recently completed run
// touched — Read/Edit/Write args from action_signatures, deduplicated,
// first-occurrence order. Unlike primer.go's previousRunFilesSection (which
// only wants Edit/Write, i.e. "what changed"), this includes Read: cache
// affinity cares about what a session's early turns are likely to touch
// again, and re-reading a file is as much a cache-warmth signal as editing
// it. Returns nil when st is nil, there's no previous run, or that run
// touched nothing.
func LastRunFiles(st *store.Store, project, sessionName string) []string {
	if st == nil {
		return nil
	}
	runs, err := st.ListRuns(project, sessionName, 1)
	if err != nil || len(runs) == 0 {
		return nil
	}
	actions, err := st.ActionsForRun(runs[0].ID)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var files []string
	for _, a := range actions {
		if a.Tool != "Read" && a.Tool != "Edit" && a.Tool != "Write" {
			continue
		}
		f := strings.TrimSpace(a.Arg)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	return files
}
