package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// TestRealZcodeAppserverTurn drives the REAL `zcode app-server --stdio`
// (auto-detected ZCode desktop install) through one turn. Opt-in:
// CM_REAL_ZCODE=1. CM_ZCODE_API_KEY must carry a Coding Plan API key
// (https://z.ai/manage-apikey/apikey-list) — the turn then must answer;
// CM_ZCODE_MODEL overrides the model. CM_ZCODE_API_KEY="-" runs keyless
// and asserts the turn fails with the runtime-headers error (transport
// check without spending plan quota).
func TestRealZcodeAppserverTurn(t *testing.T) {
	if os.Getenv("CM_REAL_ZCODE") == "" {
		t.Skip("CM_REAL_ZCODE not set")
	}
	key := os.Getenv("CM_ZCODE_API_KEY")
	if key == "" {
		key = "dummy-key-transport-check"
	}
	model := os.Getenv("CM_ZCODE_MODEL")
	if model == "" {
		model = "GLM-5.3"
	}
	dir := t.TempDir()
	var sessionKey string
	if key != "-" {
		sessionKey = key
	}
	var events []string
	s := New(Params{
		ID:          "real-zcode",
		ProjectName: "real-zcode",
		ProjectPath: dir,
		Config: config.SessionConfig{
			Runtime: config.RuntimeZcode,
			Model:   model,
			Effort:  "low",
			Name:    "real",
		},
		ZcodeAPIKey: sessionKey,
		OnEvent: func(sessionID string, ev SessionEvent) {
			if entry := ev.Entry; entry != nil {
				events = append(events, fmt.Sprintf("%s/%s: %s", entry.Level, entry.Source, entry.Message))
			}
		},
	})

	if sessionKey == "" {
		// Keyless: the guard must fail the turn with the setup error before
		// any spawn.
		_, err := s.runZcodeAppserverTurn(context.Background(), "пинг", nil, "", false)
		if err == nil || !strings.Contains(err.Error(), "z.ai Coding Plan API key") {
			t.Fatalf("keyless turn error: %v", err)
		}
		return
	}

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
	if turn.finished && turn.failed != "" {
		t.Fatalf("turn failed: %s", turn.failed)
	}
	if s.zcodeTurnText() == "" {
		for _, e := range events {
			t.Logf("event: %s", e)
		}
		t.Fatal("turn produced no text")
	}
	t.Logf("reply: %s", s.zcodeTurnText())
	_ = filepath.SkipAll // keep filepath import if assertions change
}
