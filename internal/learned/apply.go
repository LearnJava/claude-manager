package learned

import (
	"errors"
	"fmt"
	"time"

	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

// State is what Load found: the plan and the files that could not be read.
type State struct {
	Items    []Item
	Problems []string
}

// Load compares the project's lesson files with the database. It writes
// nothing into the project; the only thing it records (in the database) is that
// the two sides agree where they do, which later tells a change from a first
// meeting.
func Load(st *store.Store, project, root string, now time.Time) (*State, error) {
	files, problems, err := ReadFiles(root)
	if err != nil {
		return nil, err
	}
	db, err := Collect(st, project)
	if err != nil {
		return nil, err
	}
	synced, err := st.LearnedSyncHashes(project)
	if err != nil {
		return nil, err
	}
	items := Plan(files, db, synced)
	for _, it := range items {
		if it.Action != ActionSame {
			continue
		}
		if h := it.DB.Hash(); synced[it.DB.Ref().SyncKey()] != h {
			if err := st.SetLearnedSync(project, it.Kind, it.Key, h, now); err != nil {
				return nil, err
			}
		}
	}
	return &State{Items: items, Problems: problems}, nil
}

// Pick is one ticked item. Resolution is needed only for a conflict.
type Pick struct {
	Kind, Key, Resolution string
}

// Result reports what Apply did, one line per pick.
type Result struct {
	Done   []string
	Failed []string
}

// Apply carries out the picks: it plans afresh (the picks name items, never
// content), then for each pick writes or removes the lesson file, or imports /
// retires the lesson in the database. An item that is not in the plan any more,
// or a conflict without a resolution, is reported in Failed and nothing is done
// for it. A pick never touches an item it does not name.
func Apply(st *store.Store, project, root string, picks []Pick, now time.Time) (Result, error) {
	var res Result
	state, err := Load(st, project, root, now)
	if err != nil {
		return res, err
	}
	byRef := map[Ref]Item{}
	for _, it := range state.Items {
		byRef[Ref{it.Kind, it.Key}] = it
	}
	for _, p := range picks {
		label := fmt.Sprintf("%s %s", p.Kind, p.Key)
		it, ok := byRef[Ref{p.Kind, p.Key}]
		if !ok {
			res.Failed = append(res.Failed, label+": no longer in the plan")
			continue
		}
		action, err := it.Effective(p.Resolution)
		if err == nil && action == "" {
			err = errors.New("nothing to do")
		}
		if err == nil {
			err = execute(st, project, root, it, action, now)
		}
		if err != nil {
			res.Failed = append(res.Failed, label+": "+err.Error())
			continue
		}
		res.Done = append(res.Done, label+": "+action)
	}
	return res, nil
}

func execute(st *store.Store, project, root string, it Item, action string, now time.Time) error {
	switch action {
	case ActionExportNew, ActionExportUpd:
		l := *it.DB
		if it.File != nil {
			l.Path = it.File.Path
		}
		if err := WriteFile(root, l); err != nil {
			return err
		}
		return st.SetLearnedSync(project, it.Kind, it.Key, l.Hash(), now)
	case ActionImportNew, ActionImportUpd:
		l := *it.File
		if err := importLesson(st, project, l, now); err != nil {
			return err
		}
		if l.Kind == KindSkill && l.Body != "" {
			// The skill file normally comes with the repository; write it only
			// when it is missing, never over a copy that is there.
			if _, err := experience.WriteSkillFile(root, l.Key, l.Body, false); err != nil && !errors.Is(err, experience.ErrSkillFileExists) {
				return err
			}
		}
		return st.SetLearnedSync(project, it.Kind, it.Key, l.Hash(), now)
	case ActionRetire:
		id, err := retireLesson(st, project, Ref{it.Kind, it.Key}, now)
		if err != nil {
			return err
		}
		if it.Kind == KindSkill && id != 0 {
			if err := experience.ArchiveSkillFile(root, it.Key, id); err != nil {
				return err
			}
		}
		return st.DeleteLearnedSync(project, it.Kind, it.Key)
	case ActionRemoveFile:
		if err := RemoveFile(root, *it.File); err != nil {
			return err
		}
		return st.DeleteLearnedSync(project, it.Kind, it.Key)
	}
	return fmt.Errorf("unknown action %q", action)
}
