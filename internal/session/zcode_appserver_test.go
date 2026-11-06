package session

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// The fake app-server speaks the protocol the real one was verified to
// speak (zcode.cjs 0.17.1, probed live on 2026-11-05): NDJSON frames,
// numeric ids for responses, string ids ("server-N") for reverse requests.

type fakeZcodeServer struct {
	t      *testing.T
	conn   net.Conn
	enc    *json.Encoder
	nextID int

	gotPush bool
	// sent is signaled once the client reaches session/send (or resume).
	sent chan struct{}
	// inject carries scripted session/event payloads into the wire.
	inject chan []map[string]any
}

func newFakeZcodeServer(t *testing.T) (*fakeZcodeServer, *zcodeConn, *Session) {
	t.Helper()
	clientPipe, serverPipe := net.Pipe()
	srv := &fakeZcodeServer{
		t:      t,
		conn:   serverPipe,
		enc:    json.NewEncoder(serverPipe),
		sent:   make(chan struct{}, 1),
		inject: make(chan []map[string]any),
	}
	go srv.serve()

	s := New(Params{
		ID:          "t1",
		ProjectName: "p",
		ProjectPath: "C:/tmp/wd",
		Config: config.SessionConfig{
			Runtime: config.RuntimeZcode,
			Model:   "GLM-5.3-Flash",
			Effort:  "high",
		},
		ZcodeAPIKey: "test-key",
		OnEvent:     func(string, SessionEvent) {},
	})
	// The protocol only echoes workspace paths back, so no directory needs
	// to exist for the fake run.
	conn := newZcodeConn(clientPipe, "test-key")
	return srv, conn, s
}

func (f *fakeZcodeServer) serve() {
	dec := json.NewDecoder(f.conn)
	for {
		var msg map[string]any
		if err := dec.Decode(&msg); err != nil {
			return
		}
		id, _ := msg["id"].(float64)
		method, _ := msg["method"].(string)
		switch method {
		case "provider/updateAccountConfig":
			f.gotPush = true
			f.reply(id, map[string]any{"receivedRevision": "ok"})
		case "session/create":
			f.reply(id, map[string]any{
				"sessionId": "sess_new",
				"session":   map[string]any{"sessionId": "sess_nested"},
			})
		case "session/resume":
			f.reply(id, map[string]any{"sessionId": "sess_old"})
		case "session/subscribe", "session/send":
			select {
			case f.sent <- struct{}{}:
			default:
			}
			f.reply(id, map[string]any{"accepted": true})
		default:
			f.reply(id, map[string]any{})
		}
	}
}

func (f *fakeZcodeServer) reply(id float64, result map[string]any) {
	f.t.Helper()
	if err := f.enc.Encode(map[string]any{"id": id, "result": result}); err != nil {
		f.t.Errorf("fake server reply: %v", err)
	}
}

// waitSent blocks until the client under test has reached session/send.
func (f *fakeZcodeServer) waitSent(t *testing.T) {
	t.Helper()
	select {
	case <-f.sent:
	case <-time.After(5 * time.Second):
		t.Fatal("client never reached session/send")
	}
}

// sendEvents scripts session/event notifications onto the wire.
func (f *fakeZcodeServer) sendEvents(events []map[string]any) {
	f.t.Helper()
	for _, ev := range events {
		if err := f.enc.Encode(map[string]any{
			"method": "session/event", "params": ev,
		}); err != nil {
			f.t.Errorf("fake server event: %v", err)
		}
	}
}

func TestZcodeAccountPayloadParses(t *testing.T) {
	if !strings.HasPrefix(zcodeBuiltinRevision, "zcode-builtin:") {
		t.Fatalf("builtin revision shape: %q", zcodeBuiltinRevision)
	}
	if len(zcodeProviders) != 10 {
		t.Fatalf("providers: got %d, want 10", len(zcodeProviders))
	}
	st, ok := zcodeProviderStates[zcodePreferredProvider]
	if !ok || !st.Entitled || st.ConnKey == "" {
		t.Fatalf("preferred provider state: %+v", st)
	}
}

func TestZcodeResolveProvider(t *testing.T) {
	id, err := zcodeResolveProvider()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != zcodePreferredProvider {
		t.Fatalf("resolved %q, want preferred", id)
	}
}

// TestZcodeConnReverseRequests drives the reverse-request protocol: the
// server asks, the client must answer in the shapes the real server
// validates (probed live 2026-11-05).
func TestZcodeConnReverseRequests(t *testing.T) {
	clientPipe, serverPipe := net.Pipe()
	defer clientPipe.Close()
	defer serverPipe.Close()

	frames := make(chan map[string]any, 8)
	srvEnc := json.NewEncoder(serverPipe)
	srvDec := json.NewDecoder(serverPipe)
	go func() {
		for {
			var msg map[string]any
			if err := srvDec.Decode(&msg); err != nil {
				close(frames)
				return
			}
			frames <- msg
		}
	}()
	conn := newZcodeConn(clientPipe, "test-key")
	go conn.loop()

	ask := func(id string, method string, params map[string]any) map[string]any {
		t.Helper()
		if err := srvEnc.Encode(map[string]any{
			"id": id, "method": method, "params": params,
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-frames:
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("no answer for", method)
			return nil
		}
	}

	// 1. runtime preferences: the single required boolean
	res := ask("server-1", "session/requestRuntimePreferences",
		map[string]any{})["result"].(map[string]any)
	if res["nativeSearchEnhancementsEnabled"] != false {
		t.Fatalf("preferences answer: %v", res)
	}

	// 2. runtime headers: echo expectedAccountScope, carry the api key
	res = ask("server-2", "interaction/requestProviderRuntimeHeaders",
		map[string]any{
			"providerId":           zcodePreferredProvider,
			"expectedAccountScope": "abc123",
		})["result"].(map[string]any)
	if res["headersApplied"] != true {
		t.Fatalf("headers answer: %v", res)
	}
	auth := res["requestAuth"].(map[string]any)
	if auth["apiKey"] != "test-key" || auth["accountScope"] != "abc123" {
		t.Fatalf("requestAuth: %v", auth)
	}

	// 3. runtime headers without scope: the provider's connectionKey
	res = ask("server-3", "interaction/requestProviderRuntimeHeaders",
		map[string]any{"providerId": zcodePreferredProvider})["result"].(map[string]any)
	auth = res["requestAuth"].(map[string]any)
	if auth["accountScope"] != zcodeProviderStates[zcodePreferredProvider].ConnKey {
		t.Fatalf("fallback scope: %v", auth)
	}

	// 4. no key configured: headersApplied false
	keyless := newZcodeConn(nil, "")
	res = keyless.runtimeHeadersResult([]byte("{}"))
	if res["headersApplied"] != false {
		t.Fatalf("keyless headers answer: %v", res)
	}
}

func TestZcodeProtocolTurnHappyPath(t *testing.T) {
	srv, conn, s := newFakeZcodeServer(t)
	done := make(chan hermesTurn, 1)
	errCh := make(chan error, 1)
	go conn.loop()
	go func() {
		turn, err := s.zcodeProtocolTurn(context.Background(), conn, "C:/tmp/wd",
			"привет", "", false)
		done <- turn
		errCh <- err
	}()
	srv.waitSent(t)

	srv.sendEvents([]map[string]any{
		{"type": "part.delta", "payload": map[string]any{"field": "reasoning", "delta": "думаю"}},
		{"type": "part.delta", "payload": map[string]any{"field": "text", "delta": "пин"}},
		{"type": "part.delta", "payload": map[string]any{"field": "text", "delta": "г"}},
		{"type": "message.upserted", "payload": map[string]any{"content": "пинг"}},
		{"type": "usage.delta", "payload": map[string]any{
			"inputTokens": 10, "outputTokens": 3, "reasoningTokens": 2}},
		{"type": "turn.completed", "payload": map[string]any{}},
	})

	select {
	case turn := <-done:
		if err := <-errCh; err != nil {
			t.Fatalf("protocol turn: %v", err)
		}
		if !turn.finished {
			t.Fatal("turn not finished")
		}
		if turn.sessionID != "sess_new" {
			t.Fatalf("sessionID: %q", turn.sessionID)
		}
		if s.zcodeTurnText() != "пинг" {
			t.Fatalf("last text: %q", s.zcodeTurnText())
		}
		if !srv.gotPush {
			t.Fatal("registry push not sent")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not complete")
	}
}

func TestZcodeProtocolTurnResume(t *testing.T) {
	srv, conn, s := newFakeZcodeServer(t)
	done := make(chan hermesTurn, 1)
	errCh := make(chan error, 1)
	go conn.loop()
	go func() {
		turn, err := s.zcodeProtocolTurn(context.Background(), conn, "C:/tmp/wd",
			"продолжай", "sess_old", false)
		done <- turn
		errCh <- err
	}()
	srv.waitSent(t)
	srv.sendEvents([]map[string]any{
		{"type": "turn.completed", "payload": map[string]any{}},
	})
	select {
	case turn := <-done:
		if err := <-errCh; err != nil {
			t.Fatalf("protocol turn: %v", err)
		}
		if turn.sessionID != "sess_old" {
			t.Fatalf("resumed sessionID: %q", turn.sessionID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not complete")
	}
}

func TestZcodeProtocolTurnFailed(t *testing.T) {
	srv, conn, s := newFakeZcodeServer(t)
	done := make(chan hermesTurn, 1)
	errCh := make(chan error, 1)
	go conn.loop()
	go func() {
		turn, err := s.zcodeProtocolTurn(context.Background(), conn, "C:/tmp/wd",
			"привет", "", false)
		done <- turn
		errCh <- err
	}()
	srv.waitSent(t)
	srv.sendEvents([]map[string]any{{
		"type": "turn.failed",
		"payload": map[string]any{"error": map[string]any{
			"code": "model_request_failed", "message": "Client signing requires an API Key",
		}},
	}})
	select {
	case turn := <-done:
		if err := <-errCh; err != nil {
			t.Fatalf("protocol turn: %v", err)
		}
		if turn.failed == "" {
			t.Fatal("turn.failed not recorded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not complete")
	}
}

func TestZcodeTailBufAndChildErr(t *testing.T) {
	// The tail ring keeps only the last max bytes.
	tail := &zcodeTailBuf{max: 8}
	if n, err := tail.Write([]byte("12345")); err != nil || n != 5 {
		t.Fatalf("short write: n=%d err=%v", n, err)
	}
	if n, err := tail.Write([]byte("67890")); err != nil || n != 5 {
		t.Fatalf("second write: n=%d err=%v", n, err)
	}
	if got := tail.tail(); got != "34567890" {
		t.Fatalf("tail: %q", got)
	}

	// A live child adds nothing; an exited one adds code + stderr.
	err := errors.New("zcode: пуш реестра провайдеров не принят: EOF")
	if got := zcodeEnrichChildErr(err, nil, ""); got != err {
		t.Fatalf("nil process state must not enrich: %v", got)
	}
	// A real exited ProcessState via a quick process run.
	probe := exec.Command("cmd", "/c", "exit 3")
	_ = probe.Run()
	if got := zcodeEnrichChildErr(err, probe.ProcessState, "  boom  "); !strings.Contains(got.Error(), "код 3") || !strings.Contains(got.Error(), "stderr: boom") {
		t.Fatalf("enrich missing details: %v", got)
	}
	if got := zcodeEnrichChildErr(err, probe.ProcessState, ""); !strings.Contains(got.Error(), "код 3)") {
		t.Fatalf("enrich without stderr: %v", got)
	}
}

func TestZcodeBuiltinConfigEnv(t *testing.T) {
	if !zcodeEnvHas([]string{"A=1", "ZCODE_BUILTIN_PROVIDER_CONFIG_FILE=x"}, "ZCODE_BUILTIN_PROVIDER_CONFIG_FILE") {
		t.Fatal("existing var not detected")
	}
	if zcodeEnvHas([]string{"A=1"}, "ZCODE_BUILTIN_PROVIDER_CONFIG_FILE") {
		t.Fatal("false positive on missing var")
	}

	// Semver: numeric compare, not lexicographic ("10" > "9", "3.17.2" < "3.20").
	if !zcodeSemverLess("3.17.2", "3.20") || zcodeSemverLess("10.0.0", "9.0.0") || zcodeSemverLess("3.17.2", "3.17.2") {
		t.Fatal("semver compare wrong")
	}

	// Runtime resolver: picks the newest non-dev version with an endpoint dir.
	root := filepath.Join(t.TempDir(), "runtime", "provider", "windows-x86_64")
	mk := func(rel string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "zcode-builtin.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join("0.0.0-dev", "endpoint-aaa"))
	mk(filepath.Join("3.17.2", "endpoint-bbb"))
	mk(filepath.Join("3.16.0", "endpoint-ccc"))
	got := zcodeRuntimeBuiltinConfigPathFrom(root)
	if got != filepath.Join(root, "3.17.2", "endpoint-bbb", "zcode-builtin.json") {
		t.Fatalf("runtime resolver picked %q", got)
	}

	// Install fallback: present in the layout → resolved; absent → "".
	dir := t.TempDir()
	exe := filepath.Join(dir, "ZCode.exe")
	if got := zcodeInstallBuiltinConfigPath(exe); got != "" {
		t.Fatalf("missing file must resolve empty, got %q", got)
	}
	cfgDir := filepath.Join(dir, "resources", "config", "provider")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "zcode-builtin.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := zcodeInstallBuiltinConfigPath(exe); got != filepath.Join(cfgDir, "zcode-builtin.json") {
		t.Fatalf("resolved %q", got)
	}
}
