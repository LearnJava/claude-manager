package learned

import (
	"fmt"
	"sort"
)

// Actions of a plan item. The first four go from the database to the files or
// back and happen only when one side changed since the two last agreed; a
// conflict is when both did (or when it cannot be told) and is never settled
// without a choice.
const (
	ActionSame       = "same"
	ActionExportNew  = "export_new"  // in the database, no file yet
	ActionExportUpd  = "export_upd"  // the database moved on, the file did not
	ActionImportNew  = "import_new"  // a file the database has no live lesson for
	ActionImportUpd  = "import_upd"  // the file moved on (a pull, a revert), the database did not
	ActionRetire     = "retire"      // the file was removed (a revert), the database did not change
	ActionRemoveFile = "remove_file" // retired in the database, the file is still there
	ActionConflict   = "conflict"
)

// Resolutions of a conflict.
const (
	UseFile = "file" // the file wins: import it, or retire the lesson if there is no file
	UseDB   = "db"   // the database wins: write the file, or remove it if there is no lesson
)

// Item is one lesson in a plan.
type Item struct {
	Kind   string
	Key    string
	Title  string
	Action string
	// Status of the lesson on each side ("" = absent).
	DBStatus   string
	FileStatus string
	Detail     string
	// File and DB are the two versions; only the one(s) present are set.
	File *Lesson `json:"-"`
	DB   *Lesson `json:"-"`
}

// Plan compares the lessons found in the files with those in force in the
// database, three ways: synced holds the hash both sides had when they last
// agreed (keyed Ref.SyncKey). It is pure — no I/O. Items are sorted by kind,
// then key; same items are included so a caller can record the agreement.
func Plan(files, db []Lesson, synced map[string]string) []Item {
	fm, dm := map[Ref]*Lesson{}, map[Ref]*Lesson{}
	for i := range files {
		fm[files[i].Ref()] = &files[i]
	}
	for i := range db {
		dm[db[i].Ref()] = &db[i]
	}
	refs := map[Ref]bool{}
	for r := range fm {
		refs[r] = true
	}
	for r := range dm {
		refs[r] = true
	}
	var items []Item
	for r := range refs {
		f, d := fm[r], dm[r]
		it := Item{Kind: r.Kind, Key: r.Key, File: f, DB: d}
		if d != nil {
			it.Title, it.DBStatus = d.Title, d.Status
		}
		if f != nil {
			if it.Title == "" {
				it.Title = f.Title
			}
			it.FileStatus = f.Status
		}
		s, hasS := synced[r.SyncKey()]
		switch {
		case f != nil && d != nil:
			fh, dh := f.Hash(), d.Hash()
			switch {
			case fh == dh:
				it.Action = ActionSame
			case hasS && s == fh:
				it.Action, it.Detail = ActionExportUpd, "changed here since the file was written"
			case hasS && s == dh:
				it.Action, it.Detail = ActionImportUpd, "the file changed since it was last in sync"
			default:
				it.Action, it.Detail = ActionConflict, "the file and this machine's version differ"
				if hasS {
					it.Detail = "both the file and this machine's version changed"
				}
			}
		case d != nil: // no file
			switch {
			case !hasS:
				it.Action = ActionExportNew
			case s == d.Hash():
				it.Action, it.Detail = ActionRetire, "the file was removed"
			default:
				it.Action, it.Detail = ActionConflict, "the file was removed but the lesson changed here"
			}
		default: // file only
			switch {
			case !hasS:
				it.Action = ActionImportNew
			case s == f.Hash():
				it.Action, it.Detail = ActionRemoveFile, "the lesson was taken out of force here"
			default:
				it.Action, it.Detail = ActionConflict, "the lesson was taken out of force here but the file changed"
			}
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Key < items[j].Key
	})
	return items
}

// Effective is what applying a pick does to an item: the item's own action, or
// for a conflict the one the chosen resolution stands for. "" for same and for
// a conflict without a (valid) resolution.
func (it Item) Effective(resolution string) (string, error) {
	switch it.Action {
	case ActionSame:
		return "", nil
	case ActionConflict:
		switch {
		case resolution == UseFile && it.File != nil:
			return ActionImportUpd, nil
		case resolution == UseFile:
			return ActionRetire, nil
		case resolution == UseDB && it.DB != nil:
			return ActionExportUpd, nil
		case resolution == UseDB:
			return ActionRemoveFile, nil
		}
		return "", fmt.Errorf("%s %q differs on the two sides: choose the file or this machine's version", it.Kind, it.Key)
	}
	return it.Action, nil
}
