package experience

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidSkillName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"git-session-preamble", true},
		{"a", true},
		{"skill123", true},
		{"", false},
		{"../evil", false},
		{"evil/../../etc", false},
		{"Has-Upper", false},
		{"has space", false},
		{"has_underscore", false},
		{".hidden", false},
	}
	for _, c := range cases {
		if got := ValidSkillName(c.name); got != c.want {
			t.Errorf("ValidSkillName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestWriteSkillFile_WritesUnderClaudeSkills checks the target path and that
// the write is atomic (no stray .tmp file left behind).
func TestWriteSkillFile_WritesUnderClaudeSkills(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteSkillFile(dir, "my-skill", "---\nname: my-skill\n---\nbody", false)
	if err != nil {
		t.Fatalf("WriteSkillFile: %v", err)
	}
	want := filepath.Join(dir, ".claude", "skills", "my-skill", "SKILL.md")
	if abs, _ := filepath.Abs(want); path != abs {
		t.Errorf("path = %q, want %q", path, abs)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "---\nname: my-skill\n---\nbody" {
		t.Errorf("file content = %q", data)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stray .tmp file left behind")
	}
}

// TestWriteSkillFile_RefusesToOverwrite is the LN-10 "already exists —
// overwrite?" invariant: a second write without overwrite=true must not
// touch the file, and must report ErrSkillFileExists so the caller can
// render the inline banner.
func TestWriteSkillFile_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteSkillFile(dir, "my-skill", "v1", false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := WriteSkillFile(dir, "my-skill", "v2", false); !errors.Is(err, ErrSkillFileExists) {
		t.Fatalf("second write: err = %v, want ErrSkillFileExists", err)
	}
	path := filepath.Join(dir, ".claude", "skills", "my-skill", "SKILL.md")
	data, _ := os.ReadFile(path)
	if string(data) != "v1" {
		t.Errorf("file was overwritten: %q", data)
	}

	if _, err := WriteSkillFile(dir, "my-skill", "v2", true); err != nil {
		t.Fatalf("overwrite=true write: %v", err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "v2" {
		t.Errorf("overwrite did not take effect: %q", data)
	}
}

// TestWriteSkillFile_RejectsUnsafeName is LN-10's own acceptance test: a
// skill row whose name is not [a-z0-9-] (e.g. a path-traversal attempt) must
// never be written, regardless of overwrite.
func TestWriteSkillFile_RejectsUnsafeName(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteSkillFile(dir, "../evil", "body", false); err == nil {
		t.Fatal("WriteSkillFile(\"../evil\") succeeded, want error")
	}
	if _, err := WriteSkillFile(dir, "../evil", "body", true); err == nil {
		t.Fatal("WriteSkillFile(\"../evil\", overwrite=true) succeeded, want error")
	}
	// Nothing should have been created outside dir.
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatalf("ReadDir parent: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "evil" {
			t.Errorf("escaped write created %q next to the project dir", e.Name())
		}
	}
}

func TestSkillFilePath(t *testing.T) {
	dir := t.TempDir()
	path, err := SkillFilePath(dir, "my-skill")
	if err != nil {
		t.Fatalf("SkillFilePath: %v", err)
	}
	want := filepath.Join(dir, ".claude", "skills", "my-skill", "SKILL.md")
	if abs, _ := filepath.Abs(want); path != abs {
		t.Errorf("path = %q, want %q", path, abs)
	}
	if _, err := SkillFilePath(dir, "../evil"); err == nil {
		t.Fatal("SkillFilePath(\"../evil\") succeeded, want error")
	}
}
