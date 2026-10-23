package experience

import (
	"sort"

	"claude-manager/internal/store"
)

const backfillBatch = 500

// BackfillErrorText fills action_signatures.err_text for failed rows indexed
// before LN-35 (LEARN-TASKS.md LN-36) by re-reading each row's CLI transcript.
// Rows whose transcript or step can't be found get "" so they are not
// selected again. projectPaths maps project name -> folder. filled counts rows
// that received a non-empty text, missing the rest.
func BackfillErrorText(st *store.Store, projectPaths map[string]string) (filled, missing int, err error) {
	root := TranscriptsRoot()
	for {
		refs, err := st.ErrorRowsMissingText(backfillBatch)
		if err != nil {
			return filled, missing, err
		}
		if len(refs) == 0 {
			return filled, missing, nil
		}
		groups := map[string][]store.ErrTextRef{}
		var keys []string
		for _, r := range refs {
			k := r.Project + "\x00" + r.CLISessionID
			if _, ok := groups[k]; !ok {
				keys = append(keys, k)
			}
			groups[k] = append(groups[k], r)
		}
		sort.Strings(keys)
		for _, k := range keys {
			g := groups[k]
			texts := map[int]string{}
			if g[0].CLISessionID != "" {
				if path, ferr := FindTranscript(root, projectPaths[g[0].Project], g[0].CLISessionID); ferr == nil {
					if traj, _, rerr := ReadFrom(path, 0); rerr == nil {
						for _, s := range traj.Steps {
							if s.ResultIsError {
								texts[s.Index] = ErrorLine(s.ResultText)
							}
						}
					}
				}
			}
			for _, r := range g {
				text := texts[r.StepIndex]
				if err := st.SetActionErrText(r.ID, text); err != nil {
					return filled, missing, err
				}
				if text != "" {
					filled++
				} else {
					missing++
				}
			}
		}
	}
}
