package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
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

// Recover stops a panic from propagating past the calling goroutine and logs
// it with a full stack trace under component. Call it deferred at the top of
// every goroutine entry point:
//
//	go func() {
//	    defer logger.Recover("session.input_writer", "id", s.ID)
//	    ...
//	}()
//
// Go's default behaviour for an unrecovered panic in any goroutine is to
// terminate the whole process, regardless of which goroutine it occurred in.
// On Windows that is especially costly here: this process owns a Job Object
// per active CLI session with KILL_ON_JOB_CLOSE, so the OS closing its
// handles on exit cascades into killing every live claude CLI subprocess too.
// extra is logged alongside as additional slog key/value pairs.
func Recover(component string, extra ...any) {
	if r := recover(); r != nil {
		args := append([]any{"component", component, "panic", r, "stack", string(debug.Stack())}, extra...)
		L.Error("panic.recovered", args...)
	}
}
