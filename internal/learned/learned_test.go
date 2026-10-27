package learned

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/store"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seed puts one lesson of each kind in force into st, plus one of each kind
// that is not in force.
func seed(t *testing.T, st *store.Store, project string) {
	t.Helper()
	d := t0.Add(time.Hour)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.InsertSkill(&store.Skill{Project: project, Name: "run-gates", Status: store.SkillStatusApproved,
		DraftJSON: `{"description":"d"}`, MD: "---\nname: run-gates\n---\nbody\n", SourceJSON: `[{"tool":"Bash"}]`,
		CreatedAt: t0, ApprovedAt: &d, Origin: store.SkillOriginAuto, Reason: `{"code":"kept","before":9,"after":4}`}))
	must(st.InsertSkill(&store.Skill{Project: project, Name: "rejected-one", Status: store.SkillStatusRejected,
		DraftJSON: "{}", MD: "x", SourceJSON: "[]", CreatedAt: t0}))
	must(st.InsertFailureRule(&store.FailureRule{Project: project, ErrorKey: "go-build-fail", RuleText: "Run go vet first.",
		Status: store.FailureRuleKept, DistinctRuns: 4, ExamplesJSON: `["a"]`, CreatedAt: t0, DecidedAt: &d,
		Origin: store.FailureRuleOriginAuto, Reason: `{"code":"kept","hits_before":5,"hits_after":1}`}))
	must(st.InsertFailureRule(&store.FailureRule{Project: project, ErrorKey: "dismissed", RuleText: "no",
		Status: store.FailureRuleDismissed, CreatedAt: t0}))
	must(st.InsertHookRule(&store.HookRule{Project: project, Kind: "block_command", Param: "npm test", Hint: "use the gate",
		Status: store.HookRuleTrial, CreatedAt: t0, DecidedAt: &d}))
	must(st.InsertHookRule(&store.HookRule{Project: project, Kind: "block_command", Param: "old", Hint: "h",
		Status: store.HookRuleReverted, CreatedAt: t0}))
}

func hashes(t *testing.T, st *store.Store, project string) map[Ref]string {
	t.Helper()
	ls, err := Collect(st, project)
	if err != nil {
		t.Fatal(err)
	}
	out := map[Ref]string{}
	for _, l := range ls {
		out[l.Ref()] = l.Hash()
	}
	return out
}

func pickAll(s *State, resolution string) []Pick {
	var ps []Pick
	for _, it := range s.Items {
		if it.Action != ActionSame {
			ps = append(ps, Pick{it.Kind, it.Key, resolution})
		}
	}
	return ps
}

func TestCollectOnlyLessonsInForce(t *testing.T) {
	st := newStore(t)
	seed(t, st, "p")
	ls, err := Collect(st, "p")
	if err != nil || len(ls) != 3 {
		t.Fatalf("got %d lessons, %v", len(ls), err)
	}
	kinds := []string{ls[0].Kind, ls[1].Kind, ls[2].Kind}
	if strings.Join(kinds, ",") != "ban,rule,skill" {
		t.Fatalf("kinds %v", kinds)
	}
	if other, _ := Collect(st, "other"); len(other) != 0 {
		t.Fatal("another project's lessons leaked")
	}
	// an older approval is overridden by a newer archived row of the same name
	_ = st.InsertSkill(&store.Skill{Project: "p", Name: "run-gates", Status: store.SkillStatusArchived,
		DraftJSON: "{}", SourceJSON: "[]", CreatedAt: t0.Add(48 * time.Hour)})
	if ls, _ = Collect(st, "p"); len(ls) != 2 {
		t.Fatalf("archived skill still exported: %d", len(ls))
	}
}

func TestExportThenImportGivesTheSameState(t *testing.T) {
	root := t.TempDir()
	a, b := newStore(t), newStore(t)
	seed(t, a, "p")
	now := t0.Add(72 * time.Hour)

	s, err := Load(a, "p", root, now)
	if err != nil || len(s.Items) != 3 {
		t.Fatalf("plan: %+v %v", s, err)
	}
	for _, it := range s.Items {
		if it.Action != ActionExportNew {
			t.Fatalf("%s %s: %s", it.Kind, it.Key, it.Action)
		}
	}
	res, err := Apply(a, "p", root, pickAll(s, ""), now)
	if err != nil || len(res.Failed) != 0 || len(res.Done) != 3 {
		t.Fatalf("export: %+v %v", res, err)
	}
	ents, _ := os.ReadDir(Folder(root))
	if len(ents) != 3 {
		t.Fatalf("%d files written, want 3", len(ents))
	}

	// the exporting side now agrees with its files
	if s, _ = Load(a, "p", root, now); len(s.Items) != 3 || s.Items[0].Action != ActionSame {
		t.Fatalf("after export: %+v", s.Items)
	}

	// another machine: empty database, same folder
	s, err = Load(b, "p", root, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range s.Items {
		if it.Action != ActionImportNew {
			t.Fatalf("%s %s: %s", it.Kind, it.Key, it.Action)
		}
	}
	if res, err = Apply(b, "p", root, pickAll(s, ""), now); err != nil || len(res.Failed) != 0 {
		t.Fatalf("import: %+v %v", res, err)
	}
	ha, hb := hashes(t, a, "p"), hashes(t, b, "p")
	if len(hb) != 3 {
		t.Fatalf("imported %d lessons", len(hb))
	}
	for r, h := range ha {
		if hb[r] != h {
			t.Errorf("%v differs after the round trip", r)
		}
	}
	// the skill's file came along, the rule is in the system prompt again
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "run-gates", "SKILL.md")); err != nil {
		t.Errorf("skill file not written: %v", err)
	}
	if rs, _ := b.InjectedFailureRules("p"); len(rs) != 1 || rs[0].RuleText != "Run go vet first." || rs[0].Reason == "" {
		t.Errorf("rule not injected with its effect: %+v", rs)
	}
	if s, _ = Load(b, "p", root, now); s.Items[0].Action != ActionSame || s.Items[2].Action != ActionSame {
		t.Errorf("not in sync after import: %+v", s.Items)
	}
}

func TestLoadWritesNothingIntoTheProject(t *testing.T) {
	root := t.TempDir()
	st := newStore(t)
	seed(t, st, "p")
	if _, err := Load(st, "p", root, t0); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(root); len(ents) != 0 {
		t.Fatalf("Load created %d entries in the project", len(ents))
	}
	// and a pick for nothing does not either
	if res, _ := Apply(st, "p", root, []Pick{{KindRule, "nope", ""}}, t0); len(res.Failed) != 1 {
		t.Fatalf("unknown pick: %+v", res)
	}
	if ents, _ := os.ReadDir(root); len(ents) != 0 {
		t.Fatalf("a failed pick created %d entries", len(ents))
	}
}

func TestDivergedVersionsAreNotOverwrittenSilently(t *testing.T) {
	root := t.TempDir()
	st := newStore(t)
	seed(t, st, "p")
	s, _ := Load(st, "p", root, t0)
	if _, err := Apply(st, "p", root, pickAll(s, ""), t0); err != nil {
		t.Fatal(err)
	}
	// someone edits the rule file; this machine's version changes too
	files, _, _ := ReadFiles(root)
	var rule Lesson
	for _, f := range files {
		if f.Kind == KindRule {
			rule = f
		}
	}
	rule.Body = "Edited in the file."
	if err := WriteFile(root, rule); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.ListFailureRules("p")
	for _, r := range rows {
		if r.ErrorKey == "go-build-fail" {
			d := t0
			_ = st.UpdateFailureRule(r.ID, r.Status, "Edited here.", &d)
		}
	}
	s, _ = Load(st, "p", root, t0)
	var it Item
	for _, x := range s.Items {
		if x.Kind == KindRule {
			it = x
		}
	}
	if it.Action != ActionConflict {
		t.Fatalf("action %s, want conflict", it.Action)
	}
	// without a choice nothing happens on either side
	res, _ := Apply(st, "p", root, []Pick{{KindRule, it.Key, ""}}, t0)
	if len(res.Failed) != 1 || len(res.Done) != 0 {
		t.Fatalf("conflict without a choice: %+v", res)
	}
	if r, _ := st.ListFailureRules("p"); r[1].RuleText != "Edited here." && r[0].RuleText != "Edited here." {
		t.Fatal("database was overwritten")
	}
	if b, _ := os.ReadFile(filepath.Join(Folder(root), FileName(KindRule, it.Key))); !strings.Contains(string(b), "Edited in the file.") {
		t.Fatal("file was overwritten")
	}
	// choosing the file imports it
	if res, _ = Apply(st, "p", root, []Pick{{KindRule, it.Key, UseFile}}, t0); len(res.Done) != 1 {
		t.Fatalf("use file: %+v", res)
	}
	if rs, _ := st.InjectedFailureRules("p"); len(rs) != 1 || rs[0].RuleText != "Edited in the file." {
		t.Fatalf("file version not taken: %+v", rs)
	}
}

func TestRevertedCommitRetiresTheLesson(t *testing.T) {
	root := t.TempDir()
	st := newStore(t)
	seed(t, st, "p")
	s, _ := Load(st, "p", root, t0)
	if _, err := Apply(st, "p", root, pickAll(s, ""), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "run-gates")); err == nil {
		t.Fatal("setup: skill file should not exist yet")
	}
	// "git revert" of the commit that added the lessons: the files are gone
	if err := os.RemoveAll(Folder(root)); err != nil {
		t.Fatal(err)
	}
	s, _ = Load(st, "p", root, t0)
	for _, it := range s.Items {
		if it.Action != ActionRetire {
			t.Fatalf("%s %s: %s, want retire", it.Kind, it.Key, it.Action)
		}
	}
	if res, _ := Apply(st, "p", root, pickAll(s, ""), t0); len(res.Failed) != 0 || len(res.Done) != 3 {
		t.Fatalf("retire: %+v", res)
	}
	if rs, _ := st.InjectedFailureRules("p"); len(rs) != 0 {
		t.Fatalf("rule still in the prompt: %+v", rs)
	}
	if ls, _ := Collect(st, "p"); len(ls) != 0 {
		t.Fatalf("%d lessons still in force", len(ls))
	}
	// a retired lesson is not exported again and nothing is left to do
	if s, _ = Load(st, "p", root, t0); len(s.Items) != 0 {
		t.Fatalf("plan not empty: %+v", s.Items)
	}
}

func TestPlanActions(t *testing.T) {
	mk := func(body string) Lesson {
		return Lesson{Kind: KindRule, Key: "k", Status: store.FailureRuleKept, Body: body, Appeared: t0}
	}
	a, b := mk("a"), mk("b")
	sync := func(l Lesson) map[string]string { return map[string]string{l.Ref().SyncKey(): l.Hash()} }
	cases := []struct {
		name      string
		files, db []Lesson
		synced    map[string]string
		want      string
	}{
		{"equal", []Lesson{a}, []Lesson{a}, nil, ActionSame},
		{"db only", nil, []Lesson{a}, nil, ActionExportNew},
		{"file only", []Lesson{a}, nil, nil, ActionImportNew},
		{"db moved on", []Lesson{a}, []Lesson{b}, sync(a), ActionExportUpd},
		{"file moved on", []Lesson{b}, []Lesson{a}, sync(a), ActionImportUpd},
		{"both moved", []Lesson{b}, []Lesson{mk("c")}, sync(a), ActionConflict},
		{"differ, never synced", []Lesson{a}, []Lesson{b}, nil, ActionConflict},
		{"file removed", nil, []Lesson{a}, sync(a), ActionRetire},
		{"file removed, db changed", nil, []Lesson{b}, sync(a), ActionConflict},
		{"retired here", []Lesson{a}, nil, sync(a), ActionRemoveFile},
		{"retired here, file changed", []Lesson{b}, nil, sync(a), ActionConflict},
	}
	for _, c := range cases {
		items := Plan(c.files, c.db, c.synced)
		if len(items) != 1 || items[0].Action != c.want {
			t.Errorf("%s: %+v, want %s", c.name, items, c.want)
		}
	}
}

func TestHashIgnoresEvidenceAndLayout(t *testing.T) {
	a := Lesson{Kind: KindSkill, Key: "x", Status: "approved", Body: "b", Appeared: t0,
		Evidence: &Evidence{Runs: 3}}
	b := a
	b.Evidence = nil
	b.Appeared = t0.Add(400 * time.Millisecond)
	if a.Hash() != b.Hash() {
		t.Fatal("evidence or sub-second time changed the hash")
	}
	b.Status = "trial"
	if a.Hash() == b.Hash() {
		t.Fatal("status did not change the hash")
	}
	l, err := Parse(a.Marshal())
	if err != nil || l.Hash() != a.Hash() {
		t.Fatalf("round trip: %v", err)
	}
	if string(a.Marshal()) != string(a.Marshal()) {
		t.Fatal("marshal is not deterministic")
	}
}

func TestParseRefusesWhatItCannotTrust(t *testing.T) {
	good := Lesson{Kind: KindSkill, Key: "ok-name", Status: "approved", Body: "b", Appeared: t0}
	if _, err := Parse(good.Marshal()); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"newer format":  strings.Replace(string(good.Marshal()), `"format": 1`, `"format": 99`, 1),
		"path in name":  strings.Replace(string(good.Marshal()), "ok-name", "../../evil", 1),
		"unknown kind":  strings.Replace(string(good.Marshal()), `"skill"`, `"script"`, 1),
		"not in force":  strings.Replace(string(good.Marshal()), `"approved"`, `"archived"`, 1),
		"not json":      "hello",
		"ban key drift": `{"format":1,"kind":"ban","key":"x","status":"kept","hook_kind":"a","param":"b","appeared":"2026-10-01T12:00:00Z"}`,
	}
	for name, data := range bad {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReadFilesReportsProblemsAndKeepsGoing(t *testing.T) {
	root := t.TempDir()
	st := newStore(t)
	seed(t, st, "p")
	s, _ := Load(st, "p", root, t0)
	_, _ = Apply(st, "p", root, pickAll(s, ""), t0)
	dir := Folder(root)
	_ = os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644)
	ls, problems, err := ReadFiles(root)
	if err != nil || len(ls) != 3 || len(problems) != 1 || !strings.HasPrefix(problems[0], "broken.json") {
		t.Fatalf("%d lessons, problems %v, %v", len(ls), problems, err)
	}
	if ls, problems, err = ReadFiles(t.TempDir()); err != nil || ls != nil || problems != nil {
		t.Fatalf("missing folder: %v %v %v", ls, problems, err)
	}
}

func TestRemoveFileStaysInsideTheFolder(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim.txt")
	_ = os.WriteFile(victim, []byte("x"), 0o644)
	if err := RemoveFile(root, Lesson{Kind: KindRule, Key: "k", Path: victim}); err == nil {
		t.Fatal("removed a file outside the lessons folder")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("victim is gone")
	}
}
