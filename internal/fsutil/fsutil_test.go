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
