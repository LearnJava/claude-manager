package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// L is the application-wide structured logger.
// Defaults to stderr output so callers are safe before Init is called.
var L *slog.Logger = slog.Default()

// Init creates logDir if needed, opens logDir/app.log for appending, and
// replaces L with a text-format structured logger that writes to both the
// file and stderr (stderr is useful during `wails dev`).
// Call the returned close function when the application exits.
func Init(logDir string) (func(), error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return func() {}, fmt.Errorf("logger: mkdir %s: %w", logDir, err)
	}
	logPath := filepath.Join(logDir, "app.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}, fmt.Errorf("logger: open %s: %w", logPath, err)
	}
	w := io.MultiWriter(f, os.Stderr)
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	L = slog.New(h)
	L.Info("logger.init", "path", logPath)
	return func() { _ = f.Close() }, nil
}
