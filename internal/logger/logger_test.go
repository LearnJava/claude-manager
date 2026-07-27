package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestRecover verifies that Recover stops a panic from propagating (the
// calling goroutine survives) and logs the component, panic value and a
// stack trace.
func TestRecover(t *testing.T) {
	var buf bytes.Buffer
	orig := L
	L = slog.New(slog.NewTextHandler(&buf, nil))
	defer func() { L = orig }()

	panicked := func() (ranAfterDefer bool) {
		defer func() { ranAfterDefer = true }()
		defer Recover("test.component", "id", "abc")
		panic("boom")
	}
	if !panicked() {
		t.Fatal("Recover did not stop the panic from propagating")
	}

	out := buf.String()
	for _, want := range []string{"panic.recovered", "test.component", "boom", "id=abc", "stack="} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q, got: %s", want, out)
		}
	}
}

// TestRecoverNoPanic verifies that Recover is a no-op when there is nothing
// to recover from (the common case: the deferred call fires on normal return).
func TestRecoverNoPanic(t *testing.T) {
	var buf bytes.Buffer
	orig := L
	L = slog.New(slog.NewTextHandler(&buf, nil))
	defer func() { L = orig }()

	func() {
		defer Recover("test.component")
	}()

	if buf.Len() != 0 {
		t.Errorf("expected no log output, got: %s", buf.String())
	}
}
