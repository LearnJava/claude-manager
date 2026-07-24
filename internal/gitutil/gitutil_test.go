package gitutil

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestEnsureRepoWithCommit_FreshDirectory(t *testing.T) {
	dir := t.TempDir()

	if err := EnsureRepoWithCommit(context.Background(), dir); err != nil {
		t.Fatalf("EnsureRepoWithCommit: %v", err)
	}

	if _, err := runGit(context.Background(), dir, "rev-parse", "--is-inside-work-tree"); err != nil {
		t.Fatalf("expected dir to be a git repo: %v", err)
	}
	if _, err := runGit(context.Background(), dir, "rev-parse", "HEAD"); err != nil {
		t.Fatalf("expected a resolvable HEAD after init: %v", err)
	}
}

func TestEnsureRepoWithCommit_InitializedButNoCommits(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	if err := EnsureRepoWithCommit(context.Background(), dir); err != nil {
		t.Fatalf("EnsureRepoWithCommit: %v", err)
	}

	if _, err := runGit(context.Background(), dir, "rev-parse", "HEAD"); err != nil {
		t.Fatalf("expected a resolvable HEAD after commit: %v", err)
	}
}

func TestEnsureRepoWithCommit_AlreadyHasCommit_NoOp(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "seed").CombinedOutput(); err != nil {
		t.Fatalf("seed commit: %v: %s", err, out)
	}
	before, err := runGit(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD before: %v", err)
	}

	if err := EnsureRepoWithCommit(context.Background(), dir); err != nil {
		t.Fatalf("EnsureRepoWithCommit: %v", err)
	}

	after, err := runGit(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD after: %v", err)
	}
	if strings.TrimSpace(before) != strings.TrimSpace(after) {
		t.Fatalf("HEAD moved on an already-committed repo: before=%q after=%q", before, after)
	}
}

func TestEnsureRepoWithCommit_NotAGitBinary(t *testing.T) {
	if _, err := runGit(context.Background(), t.TempDir(), "not-a-real-subcommand"); err == nil {
		t.Fatal("expected an error for an invalid git subcommand")
	}
}
