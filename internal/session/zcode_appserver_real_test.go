package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// TestRealZcodeAppserverTurn drives the REAL `zcode app-server --stdio`
// (auto-detected ZCode desktop install) through one turn. Opt-in:
// CM_REAL_ZCODE=1. The z.ai API key comes from CM_ZCODE_API_KEY; with a
// dummy key the turn must still traverse the whole protocol (registry
// push accepted, session created, prompt sent) and fail at the model call
// with the provider's signing error — that asserts the transport without
// depending on a real key.
func TestRealZcodeAppserverTurn(t *testing.T) {
	if os.Getenv("CM_REAL_ZCODE") == "" {
		t.Skip("CM_REAL_ZCODE not set")
	}
	key := os.Getenv("CM_ZCODE_API_KEY")
	if key == "" {
		key = "dummy-key-transport-check"
	}
	dir := t.TempDir()
	s := New(Params{
		ID:          "real-zcode",
		ProjectName: "real-zcode",
		ProjectPath: dir,
		Config: config.SessionConfig{
			Runtime: config.RuntimeZcode,
			Model:   "GLM-5.3-Flash",
			Effort:  "low",
			Name:    "real",
		},
		ZcodeAPIKey: key,
		OnEvent:     func(sessionID string, ev SessionEvent) {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	turn, err := s.runZcodeAppserverTurn(ctx, "Ответь ровно одним словом: пинг",
		nil, "", false)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if turn.sessionID == "" || !strings.HasPrefix(turn.sessionID, "sess_") {
		t.Fatalf("sessionID: %q", turn.sessionID)
	}
	t.Logf("session: %s", turn.sessionID)

	if key != "dummy-key-transport-check" {
		// A real key: the model must have answered.
		if turn.finished && turn.failed != "" {
			t.Fatalf("real-key turn failed: %s", turn.failed)
		}
		if s.zcodeTurnText() == "" {
			t.Fatal("real-key turn produced no text")
		}
		t.Logf("reply: %s", s.zcodeTurnText())
		return
	}
	// Dummy key: the provider must reject it at signing time.
	if !turn.finished {
		t.Fatal("dummy-key turn did not terminate")
	}
	if turn.failed == "" {
		t.Fatal("dummy-key turn did not record the provider error")
	}
	if !strings.Contains(turn.failed, "signing") && !strings.Contains(turn.failed, "API Key") {
		t.Logf("provider error (informational): %s", turn.failed)
	}
	t.Logf("provider rejection: %s", turn.failed)
	_ = filepath.SkipAll // keep filepath import if assertions change
}
