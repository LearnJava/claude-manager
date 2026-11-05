package session

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// buildZcodeTestDB writes a small lookalike of ZCode's session database.
func buildZcodeTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE session (id TEXT, title TEXT, createdAt INTEGER, updatedAt INTEGER, workspacePath TEXT, status TEXT)`,
		`CREATE TABLE model_usage (sessionId TEXT, totalTokens INTEGER)`,
		`INSERT INTO session VALUES ('s1', 'Refactor parser', 100, 300, 'C:/work/proj', 'done')`,
		`INSERT INTO session VALUES ('s2', 'Write docs', 100, 200, 'C:/work/proj', 'active')`,
		`INSERT INTO model_usage VALUES ('s1', 45000)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestObserveZcode_ReadsSessionsAndUsage(t *testing.T) {
	path := buildZcodeTestDB(t)

	out := ObserveZcode(path)
	if out.Error != "" {
		t.Fatalf("Error = %q", out.Error)
	}
	if !out.DBExists {
		t.Fatal("DBExists = false")
	}
	if len(out.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(out.Sessions))
	}
	// Newest updatedAt first.
	first := out.Sessions[0]
	if first["id"] != "s1" || first["title"] != "Refactor parser" {
		t.Errorf("first session = %v", first)
	}
	tokens, _ := first["tokens"].(map[string]int64)
	if tokens["total_tokens"] != 45000 {
		t.Errorf("tokens = %v, want 45000", first["tokens"])
	}
	if len(out.UsageTables) != 1 || out.UsageTables[0] != "model_usage" {
		t.Errorf("usage tables = %v", out.UsageTables)
	}
}

func TestObserveZcode_MissingDB(t *testing.T) {
	out := ObserveZcode(filepath.Join(t.TempDir(), "nope.sqlite"))
	if out.DBExists {
		t.Error("DBExists = true for a missing file")
	}
	if out.Error == "" {
		t.Error("Error is empty for a missing file")
	}
}

func TestObserveZcode_SchemaChangeDegrades(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE other (x)`); err != nil {
		t.Fatal(err)
	}
	path := extractSQLitePath(t, db)
	db.Close()

	out := ObserveZcode(path)
	if out.Error == "" {
		t.Error("Error is empty when the session table is gone")
	}
	if out.Sessions == nil {
		t.Error("Sessions must stay a non-nil empty slice")
	}
}

// extractSQLitePath pulls the file name back out of the test db handle.
func extractSQLitePath(t *testing.T, db *sql.DB) string {
	t.Helper()
	row := db.QueryRow(`PRAGMA database_list`)
	var seq int
	var name, file string
	if err := row.Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	if file == "" {
		t.Fatal("temporary db has no file")
	}
	return file
}
