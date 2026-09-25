package gitutil

import (
	"os"
	"testing"
)

// TestMain gives every git process these tests spawn a commit identity, so
// they pass on a machine without a global user.name/user.email (a fresh CI
// runner, a new dev box) instead of dying with "Author identity unknown".
// Environment only — the user's git config is never touched.
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME":     "gitutil-test",
		"GIT_AUTHOR_EMAIL":    "gitutil-test@example.com",
		"GIT_COMMITTER_NAME":  "gitutil-test",
		"GIT_COMMITTER_EMAIL": "gitutil-test@example.com",
	} {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	os.Exit(m.Run())
}
