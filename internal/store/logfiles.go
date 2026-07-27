package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"claude-manager/internal/config"
)

// LogFileInfo describes one saved log file in a project's
// .claude-manager/logs directory (auto-saved on task/run completion, or via
// the manual "Export log" button).
type LogFileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// ProjectLogsDir returns <projectPath>/.claude-manager/logs — where
// auto-saved session logs are written (see SaveSessionLogFile).
func ProjectLogsDir(projectPath string) string {
	return filepath.Join(config.ProjectConfigDir(projectPath), "logs")
}

// RenderExport renders log entries in the given format ("md", "json", or
// "txt", default "txt") — shared by the manual "Export log" button
// (app.go:ExportLog) and the automatic per-run save (SaveSessionLogFile), so
// both produce identical output.
func RenderExport(sessionID string, entries []LogEntry, format string) ([]byte, error) {
	switch format {
	case "json":
		return json.MarshalIndent(struct {
			Session string     `json:"session"`
			Entries []LogEntry `json:"entries"`
		}{Session: sessionID, Entries: entries}, "", "  ")

	case "md":
		var b strings.Builder
		fmt.Fprintf(&b, "# Session log — %s\n\n", sessionID)
		fmt.Fprintf(&b, "_Saved %s, %d entries._\n\n", time.Now().Format(time.RFC3339), len(entries))
		for _, e := range entries {
			fmt.Fprintf(&b, "- `%s` **%s** %s\n",
				e.Timestamp.Format("15:04:05"),
				e.Level,
				escapeMarkdown(e.Message),
			)
			if e.ToolName != "" {
				fmt.Fprintf(&b, "  - tool: `%s` %s\n", e.ToolName, escapeMarkdown(e.ToolInput))
			}
		}
		return []byte(b.String()), nil

	default: // txt
		var b strings.Builder
		fmt.Fprintf(&b, "Session log — %s\n", sessionID)
		fmt.Fprintf(&b, "Saved %s\n\n", time.Now().Format(time.RFC3339))
		for _, e := range entries {
			fmt.Fprintf(&b, "[%s] %-7s %s\n",
				e.Timestamp.Format("15:04:05"),
				e.Level,
				e.Message,
			)
			if e.ToolName != "" {
				fmt.Fprintf(&b, "        tool=%s %s\n", e.ToolName, e.ToolInput)
			}
		}
		return []byte(b.String()), nil
	}
}

func escapeMarkdown(s string) string {
	// Single-line: collapse newlines so list items render correctly.
	return strings.NewReplacer("\r", " ", "\n", "  ").Replace(s)
}

// SaveSessionLogFile writes entries as markdown into
// <projectPath>/.claude-manager/logs/<session>-<timestamp>.md, atomically
// (temp file + rename). A no-op (empty path, nil error) when entries is
// empty or projectPath is unset — nothing worth keeping, and interactive
// sessions with no project folder (rare) have nowhere to write.
func SaveSessionLogFile(projectPath, sessionID string, entries []LogEntry) (string, error) {
	if len(entries) == 0 || strings.TrimSpace(projectPath) == "" {
		return "", nil
	}
	dir := ProjectLogsDir(projectPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("logfiles: mkdir %s: %w", dir, err)
	}
	// Gitignore the whole logs/ folder the first time it's created — these
	// are local run artifacts, not something to commit alongside the shared
	// config.toml that lives next to it.
	if err := config.EnsureGitignore(config.ProjectConfigDir(projectPath), "logs/"); err != nil {
		return "", err
	}

	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(sessionID)
	if safe == "" {
		safe = "session"
	}
	stamp := time.Now().Format("20060102-150405.000000")
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.md", safe, stamp))

	data, err := RenderExport(sessionID, entries, "md")
	if err != nil {
		return "", err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("logfiles: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("logfiles: rename %s: %w", tmp, err)
	}
	return path, nil
}

// ListProjectLogFiles returns metadata for every saved log file in a
// project's .claude-manager/logs directory, newest first. A missing
// directory returns an empty slice, not an error.
func ListProjectLogFiles(projectPath string) ([]LogFileInfo, error) {
	dir := ProjectLogsDir(projectPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("logfiles: read %s: %w", dir, err)
	}

	var files []LogFileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, LogFileInfo{
			Name:    e.Name(),
			Path:    filepath.Join(dir, e.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
	return files, nil
}

// ClearProjectLogFiles deletes every saved log file in a project's
// .claude-manager/logs directory. Returns the count removed and bytes freed;
// a missing directory is not an error.
func ClearProjectLogFiles(projectPath string) (removed int, freedBytes int64, err error) {
	files, err := ListProjectLogFiles(projectPath)
	if err != nil {
		return 0, 0, err
	}
	for _, f := range files {
		if rmErr := os.Remove(f.Path); rmErr != nil {
			err = rmErr
			continue
		}
		removed++
		freedBytes += f.Size
	}
	return removed, freedBytes, err
}
