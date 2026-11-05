package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // register the "sqlite" driver (already a direct dep)
)

// Read-only observation of ZCode sessions from the app's local database,
// ported from zsm's engines/zcode_observe.py. Today this is the only
// working way to see what the ZCode app is doing — no control channel is
// touched. The DB schema is an implementation detail, so every read degrades
// to "unknown" instead of failing.

// ZcodeObserveSnapshot is the result of ObserveZcode (never an error —
// problems land in Error, mirroring the zsm shape).
type ZcodeObserveSnapshot struct {
	DBPath      string           `json:"db_path"`
	DBExists    bool             `json:"db_exists"`
	Sessions    []map[string]any `json:"sessions"`
	UsageTables []string         `json:"usage_tables"`
	Error       string           `json:"error"`
	TS          float64          `json:"ts"`
}

// defaultZcodeDBPath is the ZCode app's session database location.
func defaultZcodeDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".zcode", "cli", "db", "db.sqlite")
}

// ObserveZcode returns a point-in-time snapshot of ZCode's sessions from the
// local database, read-only. dbPath == "" uses the default location.
func ObserveZcode(dbPath string) ZcodeObserveSnapshot {
	if dbPath == "" {
		dbPath = defaultZcodeDBPath()
	}
	out := ZcodeObserveSnapshot{
		DBPath: dbPath, DBExists: fileExists(dbPath),
		Sessions: []map[string]any{}, TS: float64(time.Now().UnixNano()) / 1e9,
	}
	if !out.DBExists {
		out.Error = "база сессий ZCode не найдена"
		return out
	}
	// mode=ro: never create or write; backslashes are not URI-safe on the
	// sqlite file: DSN, so the path goes in forward slashes.
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		out.Error = fmt.Sprintf("не открыть базу: %v", err)
		return out
	}
	defer db.Close()

	tables, err := sqliteTables(db)
	if err != nil {
		out.Error = fmt.Sprintf("чтение базы: %v", err)
		return out
	}
	if !tables["session"] {
		out.Error = "таблицы session нет — схема изменилась"
		return out
	}
	cols, err := sqliteColumns(db, "session")
	if err != nil {
		out.Error = fmt.Sprintf("чтение базы: %v", err)
		return out
	}
	var pick []string
	for _, c := range []string{"id", "title", "createdAt", "updatedAt", "workspacePath", "status"} {
		if cols[c] {
			pick = append(pick, c)
		}
	}
	orderBy := "rowid"
	if cols["updatedAt"] {
		orderBy = "updatedAt"
	}
	query := fmt.Sprintf("SELECT %s FROM session ORDER BY %s DESC LIMIT 30",
		strings.Join(pick, ", "), orderBy)
	rows, err := db.Query(query)
	if err != nil {
		out.Error = fmt.Sprintf("чтение базы: %v", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, len(pick))
		ptrs := make([]any, len(pick))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			out.Error = fmt.Sprintf("чтение базы: %v", err)
			return out
		}
		rec := map[string]any{}
		for i, c := range pick {
			rec[c] = sqliteValue(values[i])
		}
		if sid, ok := rec["id"].(string); ok {
			rec["tokens"] = sqliteModelUsage(db, tables, sid)
		}
		out.Sessions = append(out.Sessions, rec)
	}
	if err := rows.Err(); err != nil {
		out.Error = fmt.Sprintf("чтение базы: %v", err)
	}
	for t := range tables {
		if strings.Contains(t, "usage") || t == "tool_usage" {
			out.UsageTables = append(out.UsageTables, t)
		}
	}
	sort.Strings(out.UsageTables)
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sqliteTables(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables[name] = true
	}
	return tables, rows.Err()
}

func sqliteColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// sqliteModelUsage is best-effort per-session token totals when the schema
// allows them (zsm's _model_usage: model_usage or turn_usage).
func sqliteModelUsage(db *sql.DB, tables map[string]bool, sessionID string) map[string]int64 {
	for _, probe := range []struct{ table, sidCol, valCol string }{
		{"model_usage", "sessionId", "totalTokens"},
		{"turn_usage", "session_id", "total_tokens"},
	} {
		if !tables[probe.table] {
			continue
		}
		cols, err := sqliteColumns(db, probe.table)
		if err != nil || !cols[probe.sidCol] || !cols[probe.valCol] {
			continue
		}
		var sum sql.NullInt64
		query := fmt.Sprintf("SELECT SUM(%s) FROM %s WHERE %s = ?", probe.valCol, probe.table, probe.sidCol)
		if err := db.QueryRow(query, sessionID).Scan(&sum); err != nil || !sum.Valid {
			continue
		}
		return map[string]int64{"total_tokens": sum.Int64}
	}
	return nil
}

// sqliteValue normalizes a driver-typed cell into a JSON-friendly one.
func sqliteValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case int64:
		return t
	case float64:
		return t
	case nil:
		return nil
	default:
		return fmt.Sprint(v)
	}
}
