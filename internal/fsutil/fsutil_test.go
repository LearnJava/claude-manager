package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCaseInsensitive_ExactMatchUnchanged(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "STATUS-P1.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveCaseInsensitive(root, file); got != file {
		t.Errorf("got %q, want unchanged %q", got, file)
	}
}

func TestResolveCaseInsensitive_CorrectsLastComponent(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "STATUS-P4.md")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	typed := filepath.Join(root, "STATUS-P4.MD")
	got := ResolveCaseInsensitive(root, typed)
	if got != real {
		t.Errorf("got %q, want %q", got, real)
	}
}

func TestResolveCaseInsensitive_CorrectsMiddleComponent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "Tasks", "01-foo.md")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	typed := filepath.Join(root, "tasks", "01-foo.md")
	got := ResolveCaseInsensitive(root, typed)
	if got != real {
		t.Errorf("got %q, want %q", got, real)
	}
}

// On a case-sensitive filesystem two names that differ only in case are two
// files: the exact spelling must win over a case-insensitive match.
func TestResolveCaseInsensitive_ExactSpellingWinsOverFold(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"A.md", "a.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) < 2 {
		t.Skip("case-insensitive filesystem: A.md and a.md are one file")
	}
	for _, name := range []string{"A.md", "a.md"} {
		p := filepath.Join(root, name)
		if got := ResolveCaseInsensitive(root, p); got != p {
			t.Errorf("got %q, want the exact %q", got, p)
		}
	}
}

// A path that needs no correction comes back as the caller spelled it, not
// cleaned or re-joined.
func TestResolveCaseInsensitive_UnchangedPathKeepsItsSpelling(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "Q.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	typed := root + "/docs/./Q.md"
	if got := ResolveCaseInsensitive(root, typed); got != typed {
		t.Errorf("got %q, want %q unchanged", got, typed)
	}
}

func TestResolveCaseInsensitive_NoMatchReturnsUnchanged(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "NOPE.md")
	got := ResolveCaseInsensitive(root, missing)
	if got != missing {
		t.Errorf("got %q, want unchanged %q", got, missing)
	}
}

func TestResolveCaseInsensitive_NeverEscapesRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.md")
	got := ResolveCaseInsensitive(root, outside)
	if got != outside {
		t.Errorf("got %q, want unchanged %q (must not resolve outside root)", got, outside)
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{"abc-1_2.x": "abc-1_2.x", "a b/c": "a_b_c", "..": "_", "": "_", ".x.": "x"}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}
