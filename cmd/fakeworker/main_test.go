package main

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeScenario(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const minimalScenario = `{"name":"s","responses":[{"content":"hello","finish_reason":"stop"}]}`

func TestStart_ServesScenario(t *testing.T) {
	path := writeScenario(t, minimalScenario)

	var out strings.Builder
	srv, ln, err := start([]string{"-scenario", path, "-addr", "127.0.0.1:0"}, "", &out)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	url, ok := strings.CutPrefix(strings.TrimSpace(out.String()), "FAKEWORKER_URL=")
	if !ok {
		t.Fatalf("stdout missing FAKEWORKER_URL=: %q", out.String())
	}

	resp, err := http.Post(url+"/chat/completions", "application/json",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var sawContent, sawDone bool
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"hello"`) {
			sawContent = true
		}
		if strings.Contains(line, "[DONE]") {
			sawDone = true
		}
	}
	if !sawContent || !sawDone {
		t.Fatalf("stream missing content/DONE (content=%v done=%v)", sawContent, sawDone)
	}
}

func TestStart_ScenarioFromEnv(t *testing.T) {
	path := writeScenario(t, minimalScenario)

	var out strings.Builder
	srv, ln, err := start([]string{"-addr", "127.0.0.1:0"}, path, &out)
	if err != nil {
		t.Fatalf("start with env scenario: %v", err)
	}
	ln.Close()
	_ = srv
}

func TestStart_NoScenario(t *testing.T) {
	if _, _, err := start(nil, "", &strings.Builder{}); err == nil {
		t.Fatal("expected error when no scenario is given")
	}
}

func TestStart_BadScenario(t *testing.T) {
	path := writeScenario(t, `{"name":"s"}`) // no responses
	if _, _, err := start([]string{"-scenario", path}, "", &strings.Builder{}); err == nil {
		t.Fatal("expected error for invalid scenario")
	}
}
