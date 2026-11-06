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
	"sync"
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

	mu           sync.Mutex
	createParams map[string]any
	setModeCount int
	setModeMode  string

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
			p, _ := msg["params"].(map[string]any)
			f.mu.Lock()
			f.createParams = p
			f.mu.Unlock()
			f.reply(id, map[string]any{
				"sessionId": "sess_new",
				"session":   map[string]any{"sessionId": "sess_nested"},
			})
		case "session/resume":
			f.reply(id, map[string]any{"sessionId": "sess_old"})
		case "session/setMode":
			p, _ := msg["params"].(map[string]any)
			f.mu.Lock()
			f.setModeCount++
			f.setModeMode, _ = p["mode"].(string)
			f.mu.Unlock()
			f.reply(id, map[string]any{})
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

	// 5. permission ask (non-yolo modes): a valid deny decision, not the
	// empty-object default the server would fail to parse.
	res = ask("server-4", "interaction/requestPermission",
		map[string]any{"toolName": "Write"})["result"].(map[string]any)
	if res["decision"] != "deny" {
		t.Fatalf("permission answer: %v", res)
	}
}

func TestZcodePermissionModeMapping(t *testing.T) {
	cases := map[string]string{
		"":                  "yolo", // applySessionDefaults default is bypassPermissions
		"bypassPermissions": "yolo",
		"dontAsk":           "yolo",
		"acceptEdits":       "edit",
		"plan":              "plan",
		"default":           "build",
	}
	for in, want := range cases {
		if got := zcodePermissionMode(in); got != want {
			t.Errorf("zcodePermissionMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestZcodeCoalescesDeltas drives the mapper directly: streamed word
// fragments must become whole blocks (one thinking entry, one text entry),
// not per-delta log entries the feed would render as broken lines.
func TestZcodeCoalescesDeltas(t *testing.T) {
	_, _, s := newFakeZcodeServer(t)
	var turn hermesTurn
	ev := func(typ string, payload map[string]any) []*ParsedEvent {
		raw, _ := json.Marshal(payload)
		return s.zcodeMapEvent(zcodeEvent{Type: typ, Payload: raw}, &turn)
	}

	var got []config.LogEntry
	collect := func(evs []*ParsedEvent) {
		for _, pe := range evs {
			got = append(got, pe.Entries...)
		}
	}

	collect(ev("model.streaming", map[string]any{"kind": "reasoning_delta", "delta": "ду"}))
	collect(ev("model.streaming", map[string]any{"kind": "reasoning_delta", "delta": "маю"}))
	if len(got) != 0 {
		t.Fatalf("reasoning deltas emitted early: %d entries", len(got))
	}
	collect(ev("model.streaming", map[string]any{"kind": "text_delta", "delta": "при"}))
	if len(got) != 1 || got[0].Level != "thinking" || got[0].Message != "думаю" {
		t.Fatalf("thought block: %+v", got)
	}
	collect(ev("model.streaming", map[string]any{"kind": "text_delta", "delta": "вет"}))
	if len(got) != 1 {
		t.Fatalf("text deltas emitted early: %d entries", len(got))
	}
	collect(ev("turn.completed", map[string]any{
		"response": "привет",
		"usage":    map[string]any{"inputTokens": 5, "outputTokens": 2},
	}))
	// After turn.completed the buffers flushed: one text block; the result
	// entry itself must NOT repeat the streamed answer.
	var textEntries []string
	var resultMsg string
	for _, e := range got {
		if e.Level == "text" {
			textEntries = append(textEntries, e.Message)
		}
		if e.Level == "result" {
			resultMsg = e.Message
		}
	}
	if len(textEntries) != 1 || textEntries[0] != "привет" {
		t.Fatalf("text blocks: %q", textEntries)
	}
	if resultMsg != "" {
		t.Fatalf("result entry repeated streamed text: %q", resultMsg)
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
		{"type": "turn.started", "payload": map[string]any{"turnNumber": 0}},
		{"type": "model.streaming", "payload": map[string]any{"kind": "reasoning_delta", "delta": "думаю"}},
		{"type": "model.streaming", "payload": map[string]any{
			"kind": "tool_call", "toolName": "Bash",
			"input": map[string]any{"command": "echo ping"}}},
		{"type": "tool.updated", "payload": map[string]any{
			"kind": "result", "toolName": "Bash",
			"result": map[string]any{"success": true, "content": "ping"}}},
		{"type": "model.streaming", "payload": map[string]any{"kind": "text_delta", "delta": "пин"}},
		{"type": "model.streaming", "payload": map[string]any{"kind": "text_delta", "delta": "г"}},
		{"type": "session.updated", "payload": map[string]any{
			"content": "пинг", "stopReason": "stop",
			"usage": map[string]any{
				"inputTokens": 10, "outputTokens": 3, "cacheReadTokens": 4}}},
		{"type": "turn.completed", "payload": map[string]any{
			"response": "пинг", "tokenCount": 13,
			"usage": map[string]any{
				"source": "provider", "inputTokens": 10, "outputTokens": 3,
				"reasoningTokens": 2, "cacheReadTokens": 4, "cacheWriteTokens": 5}}},
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
		// The config default ("" → bypassPermissions) must reach the wire as
		// the session mode "yolo": without it the server defaults to "build"
		// and its deny-everything permission broker makes the session
		// read-only.
		srv.mu.Lock()
		mode, _ := srv.createParams["mode"].(string)
		srv.mu.Unlock()
		if mode != "yolo" {
			t.Fatalf("session/create mode: %q, want yolo (params %v)", mode, srv.createParams)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not complete")
	}
}

func TestZcodeProtocolTurnResume(t *testing.T) {
	srv, conn, s := newFakeZcodeServer(t)
	s.Config.PermissionMode = "acceptEdits"
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
		// resume's schema has no mode field, so a resumed conversation is
		// re-armed via session/setMode — without it a conversation persisted
		// under the old build default stays read-only.
		srv.mu.Lock()
		count, mode := srv.setModeCount, srv.setModeMode
		srv.mu.Unlock()
		if count != 1 || mode != "edit" {
			t.Fatalf("session/setMode: count=%d mode=%q, want 1/edit", count, mode)
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
