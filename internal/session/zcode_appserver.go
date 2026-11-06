package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
	"claude-manager/internal/proc"
)

// The ZCode app-server transport: the manager spawns `zcode app-server
// --stdio` (the NDJSON protocol the ZCode desktop itself drives its agent
// with) and runs one protocol session per turn — session/create or
// session/resume, session/send with a modelSelection, then session/event
// notifications mapped onto the shared ParsedEvent format until the turn
// terminates. This replaces the file transport (the .zsm inbox/outbox
// bridge): no external ZCode conversation has to be open any more — the
// manager talks to the agent runtime directly.
//
// Two server→client (reverse) requests must be answered for a turn to work:
//   - session/requestRuntimePreferences: app preferences (one boolean);
//   - interaction/requestProviderRuntimeHeaders: the Coding Plan API key
//     the runtime sends as X-Coding-Plan-Api-Key on model requests
//     (Settings.ZcodeApiKey, env ZAI_API_KEY). The key is created at
//     https://z.ai/manage-apikey/apikey-list bound to the Coding Plan —
//     a plain pay-as-you-go key authenticates but fails every model call
//     with [1113] "Insufficient balance" (verified live 2026-11-06).
//
// The model provider comes from the account registry push (see
// zcodeAccountRevision): a coding-plan provider entitled for this account.
// The payload is versioned to the ZCode build it was captured from; a
// ZCode update that bumps the builtin revision makes the push (and the
// turn) fail with a clear error instead of misbehaving silently. (A
// push-less "child signs itself" mode does not exist on 0.17.x: a
// standalone app-server child resolves no models at all.)

const (
	// zcodePreferredProvider is the coding-plan provider used when entitled
	// (matches the account this payload was captured from); any other
	// entitled zai-* provider from the payload works as a fallback.
	zcodePreferredProvider = "account:zai-individual-coding-plan"
	zcodeDefaultModel      = "GLM-5.3"
	zcodeDeliveryKind      = "desktop-continuous"
)

// zcodeAccountRevision is the account provider config the ZCode desktop
// pushes into its app-server children (captured 2026-11-05 from the
// desktop host log, CLI 0.17.1 / builtin revision 43). It is the JSON
// tuple [builtinRevision, [provider configs], [provider states]] prefixed
// with "account:" — sent verbatim as provider/updateAccountConfig's
// revision, and parsed at init into the providers/states maps.
const zcodeAccountRevision = `account:["zcode-builtin:43:3db2e911c98edc61d04c1d83ca3bbeb5f96c0c76bf1332b02aa8b67f88935374",[{"providerId":"account:zai-individual-coding-plan","config":{"access":{"type":"zhipu-account","entitled":true}}},{"providerId":"account:zai-team-coding-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},{"providerId":"account:zai-start-plan","config":{"access":{"type":"zhipu-account","entitled":true},"builtinModelIds":["GLM-5.3-Flash"]}},{"providerId":"account:bigmodel-individual-coding-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},{"providerId":"account:bigmodel-team-coding-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},{"providerId":"account:bigmodel-start-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},{"providerId":"account:zai-offpeak-idle-plan","config":{"access":{"type":"zhipu-account","entitled":true}}},{"providerId":"account:bigmodel-offpeak-idle-plan","config":{"access":{"type":"zhipu-account","entitled":false}}},{"providerId":"account:zai-highspeed-card","config":{"access":{"type":"zhipu-account","entitled":true}}},{"providerId":"account:bigmodel-highspeed-card","config":{"access":{"type":"zhipu-account","entitled":false}}}],{"account:zai-individual-coding-plan":{"availability":"available","entitled":true,"current":true,"connectionKey":"7234ee9645a3fbdbe9aa450aacede2d8cf9761d91ba402412c79a58fe0d80b9b"},"account:zai-team-coding-plan":{"availability":"unknown","entitled":false,"current":false,"connectionKey":"7234ee9645a3fbdbe9aa450aacede2d8cf9761d91ba402412c79a58fe0d80b9b"},"account:zai-start-plan":{"availability":"available","entitled":true,"current":true,"connectionKey":"96a664e80b970a563681983c1aff0002cd7c8755ac3b3006cfe43eae9e99043e"},"account:bigmodel-individual-coding-plan":{"availability":"unavailable","entitled":false,"unavailableReason":"not-connected","current":false,"connectionKey":"145736c8450a04bf4ba21aee47794f53d87ea79c5dc4cc5b9f27fbf5b6038d53"},"account:bigmodel-team-coding-plan":{"availability":"unavailable","entitled":false,"unavailableReason":"not-connected","current":false,"connectionKey":"145736c8450a04bf4ba21aee47794f53d87ea79c5dc4cc5b9f27fbf5b6038d53"},"account:bigmodel-start-plan":{"availability":"unavailable","entitled":false,"unavailableReason":"not-connected","current":false,"connectionKey":"116fde2fcda0bb10f88164a63caadb0a0ec8611edcf75b35a9924684f9ba84bd"},"account:zai-offpeak-idle-plan":{"availability":"available","entitled":true},"account:bigmodel-offpeak-idle-plan":{"availability":"unavailable","entitled":false},"account:zai-highspeed-card":{"availability":"available","entitled":true},"account:bigmodel-highspeed-card":{"availability":"unavailable","entitled":false}}]`

var (
	// zcodeBuiltinRevision is tuple[0] — echoed back as the push's
	// basedOnZCodeBuiltinRevision.
	zcodeBuiltinRevision string
	// zcodeProviders is tuple[1] as {providerId: config} — the push body.
	zcodeProviders map[string]json.RawMessage
	// zcodeProviderStatesRaw is tuple[2] verbatim — the push body.
	zcodeProviderStatesRaw map[string]json.RawMessage
	// zcodeProviderStates is tuple[2] decoded — lookups for entitlement and
	// the connectionKey (the accountScope the runtime-headers answer echoes).
	zcodeProviderStates map[string]struct {
		Entitled bool   `json:"entitled"`
		ConnKey  string `json:"connectionKey"`
	}
)

func init() {
	var tuple []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(zcodeAccountRevision, "account:")), &tuple); err != nil {
		panic("session: zcodeAccountRevision payload does not parse: " + err.Error())
	}
	if len(tuple) != 3 {
		panic("session: zcodeAccountRevision is not a 3-tuple")
	}
	if err := json.Unmarshal(tuple[0], &zcodeBuiltinRevision); err != nil {
		panic("session: zcodeAccountRevision builtin revision: " + err.Error())
	}
	var providerList []struct {
		ProviderID string          `json:"providerId"`
		Config     json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(tuple[1], &providerList); err != nil {
		panic("session: zcodeAccountRevision providers: " + err.Error())
	}
	zcodeProviders = map[string]json.RawMessage{}
	for _, p := range providerList {
		zcodeProviders[p.ProviderID] = p.Config
	}
	if err := json.Unmarshal(tuple[2], &zcodeProviderStatesRaw); err != nil {
		panic("session: zcodeAccountRevision states: " + err.Error())
	}
	if err := json.Unmarshal(tuple[2], &zcodeProviderStates); err != nil {
		panic("session: zcodeAccountRevision states decode: " + err.Error())
	}
}

// zcodeResolveProvider picks the coding-plan provider to send model
// selections under: the preferred one when entitled, else any entitled
// zai-* provider in the pushed registry.
func zcodeResolveProvider() (string, error) {
	if st, ok := zcodeProviderStates[zcodePreferredProvider]; ok && st.Entitled {
		return zcodePreferredProvider, nil
	}
	for id, st := range zcodeProviderStates {
		if st.Entitled && strings.HasPrefix(id, "account:zai-") {
			return id, nil
		}
	}
	return "", errors.New("zcode: в пуше реестра нет ни одного доступного z.ai-плана — обнови zcodeAccountRevision (payload привязан к версии ZCode)")
}

// zcodePermissionMode maps the manager's permission_mode config onto the
// app-server's session mode enum (zcode.cjs 0.17.x: plan|build|edit|yolo|
// guarded|auto). "yolo" is the wire form of Full access — the engine allows
// every tool without the permission broker ("Yolo mode bypasses permission
// prompts"). The server default is "build", which routes every non-read tool
// to the broker, and a stdio client with no registered permission client
// gets the deny-everything broker ("No permission client configured for
// Write") — the read-only symptom this mapping exists to fix. "" maps to
// yolo because applySessionDefaults already defaults PermissionMode to
// bypassPermissions; "auto" is never returned (server: "reserved but not
// implemented yet" → deny).
func zcodePermissionMode(pm string) string {
	switch pm {
	case "acceptEdits":
		return "edit"
	case "plan":
		return "plan"
	case "default":
		return "build"
	default: // bypassPermissions, dontAsk, "" (config default)
		return "yolo"
	}
}

// zcodeEnvHas reports whether the k=v list already carries key.
func zcodeEnvHas(env []string, key string) bool {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}

// zcodeSemverLess compares dotted numeric versions ("3.17.2" < "3.20.1");
// non-numeric segments compare lexicographically.
func zcodeSemverLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		if i < len(as) {
			an, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bn, _ = strconv.Atoi(bs[i])
		}
		if an != bn {
			return an < bn
		}
	}
	return false
}

// zcodeRuntimeBuiltinConfig finds the desktop's refreshed builtin provider
// config: ~/.zcode/v2/runtime/provider/windows-x86_64/<ver>/endpoint-*/zcode-
// builtin.json, newest non-dev version. The install-shipped copy (checked
// first as a fallback) is NOT equivalent: only the runtime copy carries the
// model rules for the account-plan providers — without them the registry
// rejects every model selection ("Provider Registry 中不存在 Provider").
func zcodeBuiltinConfigPath(exe string) string {
	if p := zcodeRuntimeBuiltinConfigPath(); p != "" {
		return p
	}
	return zcodeInstallBuiltinConfigPath(exe)
}

// zcodeInstallBuiltinConfigPath resolves the copy shipped inside the install
// (resources/config/provider/zcode-builtin.json, next to the detected
// binary) — the fallback when the desktop has no runtime copy yet.
func zcodeInstallBuiltinConfigPath(exe string) string {
	p := filepath.Join(filepath.Dir(exe), "resources", "config", "provider", "zcode-builtin.json")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func zcodeRuntimeBuiltinConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return zcodeRuntimeBuiltinConfigPathFrom(filepath.Join(home, ".zcode", "v2", "runtime", "provider", "windows-x86_64"))
}

func zcodeRuntimeBuiltinConfigPathFrom(root string) string {
	vers, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	best := ""
	for _, v := range vers {
		name := v.Name()
		if !v.IsDir() || strings.HasPrefix(name, "0.0.0") { // dev trees are stale copies
			continue
		}
		if best != "" {
			cur := filepath.Base(filepath.Dir(filepath.Dir(best)))
			if !zcodeSemverLess(cur, name) {
				continue
			}
		}
		eps, err := os.ReadDir(filepath.Join(root, name))
		if err != nil {
			continue
		}
		for _, ep := range eps {
			if !ep.IsDir() || !strings.HasPrefix(ep.Name(), "endpoint-") {
				continue
			}
			p := filepath.Join(root, name, ep.Name(), "zcode-builtin.json")
			if _, err := os.Stat(p); err == nil {
				best = p
			}
		}
	}
	return best
}

// zcodePersonalConfigPath resolves the desktop's personal provider config
// (~/.zcode/v2/provider_config.json) — the second half of the Built-in /
// Personal pair the server needs; without it the account-plan providers are
// missing from the registry view even with the builtin config present.
func zcodePersonalConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(home, ".zcode", "v2", "provider_config.json")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// zcodeDetectBinary locates the ZCode desktop install (the CLI core ships
// inside it: Electron-as-Node runs resources/glm/zcode.cjs).
func zcodeDetectBinary() (exe, cjs string, err error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", "", e
		}
		local = filepath.Join(home, "AppData", "Local")
	}
	exe = filepath.Join(local, "Programs", "ZCode", "ZCode.exe")
	cjs = filepath.Join(local, "Programs", "ZCode", "resources", "glm", "zcode.cjs")
	if _, e := os.Stat(exe); e != nil {
		return "", "", fmt.Errorf("zcode: не найден ZCode.exe (%s) — установи ZCode или пропиши zcode_path в config.toml", exe)
	}
	if _, e := os.Stat(cjs); e != nil {
		return "", "", fmt.Errorf("zcode: не найден zcode.cjs (%s) — структура установки ZCode изменилась", cjs)
	}
	return exe, cjs, nil
}

// ---- protocol connection ----

type zcodeWireError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *zcodeWireError) Error() string { return e.Message }

// zcodeMessage is one NDJSON frame. Responses carry a numeric ID plus
// Result/Error; notifications carry Method only; server→client requests
// carry a string ID (like "server-1") plus Method+Params.
type zcodeMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *zcodeWireError `json:"error"`
}

// zcodeEvent is a session/event notification's envelope (payload stays
// raw; the mapper decodes per type).
type zcodeEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Payload   json.RawMessage `json:"payload"`
}

// zcodeConn is the client half of the app-server protocol. One goroutine
// (loop) reads every frame: responses unblock pending calls, notifications
// dispatch, reverse requests answer inline.
type zcodeConn struct {
	rw io.ReadWriteCloser

	mu      sync.Mutex // guards enc + nextID + pending
	enc     *json.Encoder
	nextID  int
	pending map[int]chan zcodeMessage

	apiKey string // signs model requests; empty = turn fails at the model call

	onEvent func(zcodeEvent) // every session/event notification
}

func newZcodeConn(rw io.ReadWriteCloser, apiKey string) *zcodeConn {
	return &zcodeConn{
		rw:      rw,
		enc:     json.NewEncoder(rw),
		pending: map[int]chan zcodeMessage{},
		apiKey:  apiKey,
	}
}

func (c *zcodeConn) send(m map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(m)
}

// loop reads frames until the connection closes, then unblocks every
// pending call with the read error. Run in its own goroutine.
func (c *zcodeConn) loop() {
	dec := json.NewDecoder(c.rw)
	for {
		var msg zcodeMessage
		if err := dec.Decode(&msg); err != nil {
			c.failPending(err)
			return
		}
		if msg.Method != "" && len(msg.ID) > 0 && string(msg.ID) != "null" {
			// A server→client request: answer it. msg.ID is raw JSON —
			// unquote it (RawMessage keeps the surrounding quotes).
			reqID := strings.Trim(string(msg.ID), `"`)
			c.answerServerRequest(reqID, msg.Method, msg.Params)
			continue
		}
		if msg.Method != "" {
			if msg.Method == "session/event" && c.onEvent != nil {
				var ev zcodeEvent
				if json.Unmarshal(msg.Params, &ev) == nil {
					c.onEvent(ev)
				}
			}
			continue
		}
		// A response: unblock its caller.
		var id int
		if json.Unmarshal(msg.ID, &id) != nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ok {
			ch <- msg
		}
	}
}

func (c *zcodeConn) failPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- zcodeMessage{ID: json.RawMessage(fmt.Sprint(id)), Error: &zcodeWireError{Message: err.Error()}}
		delete(c.pending, id)
	}
}

// call sends a request and waits for its response (or ctx cancellation).
func (c *zcodeConn) call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan zcodeMessage, 1)
	c.pending[id] = ch
	req := map[string]any{"id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	err := c.enc.Encode(req)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			if len(msg.Error.Data) > 0 {
				var d struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(msg.Error.Data, &d) == nil && d.Message != "" {
					return fmt.Errorf("%s: %s", msg.Error.Message, d.Message)
				}
			}
			return msg.Error
		}
		if result != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}

// answerServerRequest replies to the server's reverse requests. Unknown
// ones get an empty result: better a validation error on that one request
// than a hung turn.
func (c *zcodeConn) answerServerRequest(id, method string, params json.RawMessage) {
	var result any
	switch method {
	case "session/requestRuntimePreferences":
		result = map[string]any{"nativeSearchEnhancementsEnabled": false}
	case "interaction/requestProviderRuntimeHeaders":
		result = c.runtimeHeadersResult(params)
	case "interaction/requestOfficialMcpAuthHeaders":
		result = map[string]any{"headersApplied": false}
	case "interaction/requestPermission":
		// Only reachable in non-yolo modes: the manager registers no
		// interactive permission gate on this wire, so the honest answer is
		// a valid deny decision (enum allow|deny|escalate|modify) instead
		// of the empty-object default the server would fail to parse.
		result = map[string]any{
			"decision": "deny",
			"reason":   "claude-manager: no interactive permission client on the app-server wire",
		}
	default:
		result = map[string]any{}
	}
	_ = c.send(map[string]any{"id": id, "result": result})
}

func (c *zcodeConn) runtimeHeadersResult(params json.RawMessage) map[string]any {
	if c.apiKey == "" {
		return map[string]any{"headersApplied": false}
	}
	var prm struct {
		ProviderID           string `json:"providerId"`
		ExpectedAccountScope string `json:"expectedAccountScope"`
	}
	_ = json.Unmarshal(params, &prm)
	scope := prm.ExpectedAccountScope
	if scope == "" {
		if st, ok := zcodeProviderStates[prm.ProviderID]; ok {
			scope = st.ConnKey
		}
	}
	return map[string]any{
		"headersApplied": true,
		"requestAuth": map[string]any{
			"apiKey":       c.apiKey,
			"accountScope": scope,
		},
	}
}

// ---- the turn ----

// RunTurn drives one app-server turn (TurnRunner for the zcode runtime).
func (zcodeRuntime) RunTurn(s *Session, ctx context.Context, prompt string, images []ImageAttachment, convID string, autonomous bool) (hermesTurn, error) {
	return s.runZcodeAppserverTurn(ctx, prompt, images, convID, autonomous)
}

// zcodeCreateResult is session/create's response (only the fields the
// manager reads; the sessionId sits at one of two depths across builds).
type zcodeCreateResult struct {
	SessionID string `json:"sessionId"`
	Session   struct {
		SessionID string `json:"sessionId"`
	} `json:"session"`
}

func (s *Session) runZcodeAppserverTurn(ctx context.Context, prompt string, images []ImageAttachment, convID string, autonomous bool) (hermesTurn, error) {
	var turn hermesTurn
	if len(images) > 0 {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "system", Source: "manager",
			Message: "zcode runtime does not accept image attachments in this build — sending text only",
		}})
	}
	if s.zcodeAPIKey == "" {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "system", Source: "manager",
			Message: "z.ai Coding Plan API key не задан: добавь zcode_api_key в [settings] config.toml (или переменную ZAI_API_KEY); нужен ключ Coding Plan с https://z.ai/manage-apikey/apikey-list — обычный PAYG-ключ даёт [1113] Insufficient balance",
		}})
		return turn, errors.New("zcode: не задан z.ai Coding Plan API key (zcode_api_key)")
	}

	exe, cjs, err := zcodeDetectBinary()
	if err != nil {
		return turn, err
	}

	workDir := s.runDir()
	s.mu.Lock()
	s.zcodeLastText = "" // fresh turn: drop the previous turn's snapshot
	s.mu.Unlock()
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	cmd := exec.CommandContext(turnCtx, exe, cjs, "app-server", "--stdio")
	proc.HideConsole(cmd)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = append(os.Environ(), "ELECTRON_RUN_AS_NODE=1")
	// The app-server locates the provider configs through two env vars the
	// ZCode desktop exports to its own children, but a manager started from
	// a .bat does not have them. Without the builtin path the server exits
	// at startup ("无法定位 CLI ZCode Built-in Provider Config", exit code 1);
	// without the personal path the account-plan providers never enter the
	// registry view and every model selection fails with
	// "Provider Registry 中不存在 Provider". Both proven by env bisect +
	// single-var trials on 2026-11-06.
	if !zcodeEnvHas(cmd.Env, "ZCODE_BUILTIN_PROVIDER_CONFIG_FILE") {
		if p := zcodeBuiltinConfigPath(exe); p != "" {
			cmd.Env = append(cmd.Env, "ZCODE_BUILTIN_PROVIDER_CONFIG_FILE="+p)
		}
	}
	if !zcodeEnvHas(cmd.Env, "ZCODE_PERSONAL_PROVIDER_CONFIG_FILE") {
		if p := zcodePersonalConfigPath(); p != "" {
			cmd.Env = append(cmd.Env, "ZCODE_PERSONAL_PROVIDER_CONFIG_FILE="+p)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return turn, fmt.Errorf("stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return turn, fmt.Errorf("stdin pipe: %w", err)
	}
	// app-server logs to stdout; stderr stays empty in normal operation but
	// names the cause when the child dies at startup — keep its tail.
	stderrTail := &zcodeTailBuf{max: 4096}
	cmd.Stderr = stderrTail
	if err := cmd.Start(); err != nil {
		return turn, fmt.Errorf("start zcode app-server: %w", err)
	}
	logger.L.Info("session.spawned", "id", s.ID, "pid", cmd.Process.Pid, "runtime", s.runtime.Name(), "protocol", "app-server")
	s.beginStartWatch(cmd.Process.Pid)
	defer s.endStartWatch()

	job, jobErr := proc.NewJob()
	if jobErr != nil {
		logger.L.Warn("session.job_create_failed", "id", s.ID, "error", jobErr)
	}
	defer job.Close()
	if err := job.Assign(cmd.Process.Pid); err != nil {
		logger.L.Warn("session.job_assign_failed", "id", s.ID, "pid", cmd.Process.Pid, "error", err)
	}
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cmd = nil
		s.mu.Unlock()
	}()

	if prompt != "" {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "user", Source: "zcode", Message: prompt,
		}})
	}

	pipeRW := struct {
		io.Reader
		io.WriteCloser
	}{stdout, stdin}
	conn := newZcodeConn(pipeRW, s.zcodeAPIKey)
	go conn.loop()

	turn, err = s.zcodeProtocolTurn(turnCtx, conn, workDir, prompt, convID, autonomous)
	if err != nil {
		// Protocol failure — usually the child dying (bare EOF says nothing).
		// Wait briefly for it and append the exit code + stderr tail so the
		// error names the actual cause; if it is still running, keep the
		// error as is and let the cleanup below tear it down.
		waited := make(chan struct{})
		go func() { defer close(waited); _ = cmd.Wait() }()
		select {
		case <-waited:
			err = zcodeEnrichChildErr(err, cmd.ProcessState, stderrTail.tail())
		case <-time.After(2 * time.Second):
		}
	}
	cancelTurn()
	_ = stdin.Close()
	_ = cmd.Wait()
	return turn, err
}

// zcodeTailBuf is an io.Writer keeping the last max bytes — a tail ring for
// the child's stderr.
type zcodeTailBuf struct {
	buf []byte
	max int
}

func (t *zcodeTailBuf) Write(p []byte) (int, error) {
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		copy(t.buf, t.buf[over:])
		t.buf = t.buf[:t.max]
	}
	return len(p), nil
}

func (t *zcodeTailBuf) tail() string { return string(t.buf) }

// zcodeEnrichChildErr appends the app-server's exit status and the stderr
// tail to a protocol error; with nothing to add it returns err unchanged.
func zcodeEnrichChildErr(err error, ps *os.ProcessState, stderrTail string) error {
	if ps == nil || !ps.Exited() {
		return err
	}
	detail := fmt.Sprintf(" (процесс завершён, код %d", ps.ExitCode())
	if t := strings.TrimSpace(stderrTail); t != "" {
		r := []rune(t)
		if len(r) > 300 {
			r = r[:300]
		}
		detail += "; stderr: " + string(r)
	}
	return fmt.Errorf("%w%s)", err, detail)
}

// zcodeProtocolTurn is the spawn-independent protocol phase of an
// app-server turn: registry push → create/resume → subscribe → send →
// stream events until the terminal one. Split out so tests can drive it
// over a net.Pipe against a scripted fake server.
func (s *Session) zcodeProtocolTurn(ctx context.Context, conn *zcodeConn, workDir, prompt, convID string, autonomous bool) (hermesTurn, error) {
	var turn hermesTurn
	workspace := map[string]any{"workspacePath": workDir, "workspaceKey": workDir}

	// The mapper emits into the session and finishes the turn on the first
	// terminal event (turn.completed/failed/terminal via handleEvent).
	// It returns a slice: phase-buffer flushes may precede the event's own
	// entry (see zcodeMapEvent).
	turnDone := make(chan struct{})
	var once sync.Once
	conn.onEvent = func(ev zcodeEvent) {
		for _, res := range s.zcodeMapEvent(ev, &turn) {
			if s.handleEvent(*res, autonomous) {
				once.Do(func() {
					turn.finished = true
					close(turnDone)
				})
				break // terminal: nothing after it matters
			}
		}
	}

	fail := func(err error) (hermesTurn, error) {
		if ctx.Err() != nil {
			return turn, nil
		}
		return turn, err
	}

	// 1. Account registry push (idempotent; payload matches this ZCode
	//    build). Mandatory: on ZCode 0.17.x a standalone app-server child
	//    resolves no models on its own ("Select a model before continuing",
	//    model.available: [] — probed live 2026-11-06); only the host-side
	//    push makes account-plan providers selectable. The push also puts
	//    the child into host-managed auth: the model call then requires
	//    interaction/requestProviderRuntimeHeaders, whose apiKey the
	//    runtime sends as X-Coding-Plan-Api-Key — a Coding Plan API key,
	//    NOT a pay-as-you-go key (those fail with [1113] "Insufficient
	//    balance", verified live).
	if err := conn.call(ctx, "provider/updateAccountConfig", map[string]any{
		"revision":                    zcodeAccountRevision,
		"basedOnZCodeBuiltinRevision": zcodeBuiltinRevision,
		"providers":                   zcodeProviders,
		"states":                      zcodeProviderStatesRaw,
	}, nil); err != nil {
		return fail(fmt.Errorf("zcode: пуш реестра провайдеров не принят: %w (ZCode обновился? обнови zcodeAccountRevision)", err))
	}

	// 2. Provider for model selections.
	providerID, err := zcodeResolveProvider()
	if err != nil {
		return fail(err)
	}

	// 3. Fresh session or resume of the previous turn's conversation.
	// Both carry the permission mode: create takes it inline, while resume
	// (whose schema has no mode field) is followed by session/setMode so a
	// conversation persisted under the old build default comes up with the
	// session's configured mode too.
	mode := zcodePermissionMode(s.Config.PermissionMode)
	if convID == "" {
		var created zcodeCreateResult
		if err := conn.call(ctx, "session/create", map[string]any{
			"workspace": workspace,
			"mode":      mode,
		}, &created); err != nil {
			return fail(fmt.Errorf("zcode: session/create: %w", err))
		}
		turn.sessionID = created.SessionID
		if turn.sessionID == "" {
			turn.sessionID = created.Session.SessionID
		}
	} else {
		if err := conn.call(ctx, "session/resume", map[string]any{
			"sessionId": convID,
			"workspace": workspace,
		}, nil); err != nil {
			return fail(fmt.Errorf("zcode: session/resume %s: %w", convID, err))
		}
		turn.sessionID = convID
		// Non-fatal on purpose: a setMode rejection must not kill the turn,
		// the conversation just keeps its persisted mode (reads still work).
		if err := conn.call(ctx, "session/setMode", map[string]any{
			"sessionId": convID,
			"mode":      mode,
		}, nil); err != nil {
			logger.L.Warn("session.zcode_setmode_failed", "id", s.ID, "conversation", convID, "mode", mode, "error", err)
		}
	}
	if turn.sessionID == "" {
		return fail(errors.New("zcode: session/create вернул пустой sessionId"))
	}
	// The protocol handshake IS the zcode "init": stop the start-watch
	// narration right here instead of promising an initialization phase
	// this runtime does not have.
	s.mu.Lock()
	if w := s.startW; w != nil {
		w.gotInit()
	}
	s.mu.Unlock()

	// 4. Subscribe to the event stream (live delivery of the turn).
	if err := conn.call(ctx, "session/subscribe", map[string]any{
		"sessionId": turn.sessionID, "deliveryKind": zcodeDeliveryKind,
	}, nil); err != nil {
		return fail(fmt.Errorf("zcode: session/subscribe: %w", err))
	}

	// 5. The prompt with the model + effort selection.
	s.mu.Lock()
	model := s.activeModel
	s.mu.Unlock()
	if model == "" {
		model = s.Config.Model
	}
	if model == "" {
		model = zcodeDefaultModel
	}
	sel := map[string]any{
		"providerId": providerID,
		"modelId":    model,
	}
	if effort := s.Config.Effort; effort != "" {
		sel["options"] = map[string]any{"reasoningLevel": effort}
	}
	if err := conn.call(ctx, "session/send", map[string]any{
		"sessionId":      turn.sessionID,
		"content":        prompt,
		"modelSelection": sel,
	}, nil); err != nil {
		return fail(fmt.Errorf("zcode: session/send: %w", err))
	}
	s.setActivity(Activity{Kind: ActivityThinking})

	// 6. Wait for the terminal event, the connection dying, or cancellation.
	select {
	case <-turnDone:
		return turn, nil
	case <-ctx.Done():
		return turn, nil
	}
}

// ---- event mapping ----

// zcodeFlushMin: streamed deltas accumulate in per-turn buffers and are
// emitted as whole blocks — one per reasoning/text phase — because the
// GUI renders each log entry as its own paragraph (consecutive entries
// join with a blank line, which would shatter word fragments). Long
// monologues still stream: past this size a buffer flushes at its last
// newline, a boundary the paragraph rendering already implies.
const zcodeFlushMin = 400

// zcodeMapEvent maps one session/event onto the manager's ParsedEvents
// (empty = nothing to report). Real 0.17.1 taxonomy (probed live
// 2026-11-06, .tmp/probe_full.mjs): text streams through model.streaming
// payloads {kind:"text_delta", delta}; session.updated is multipurpose —
// a payload with content+usage is the authoritative assistant-message
// snapshot; turn.completed {response, usage} ends the turn with the
// final text and provider usage; turn.failed {error{message,code,detail}}
// carries the failure (e.g. [1113]). Phase-buffer flushes may precede the
// event's own entries (a tool call closes the open text block first).
func (s *Session) zcodeMapEvent(ev zcodeEvent, turn *hermesTurn) []*ParsedEvent {
	now := time.Now()
	switch ev.Type {
	case "turn.started":
		return []*ParsedEvent{{Activity: &Activity{Kind: ActivityThinking}}}

	case "model.streaming":
		var p struct {
			Kind     string          `json:"kind"`
			Delta    string          `json:"delta"`
			ToolName string          `json:"toolName"`
			Input    json.RawMessage `json:"input"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil {
			return nil
		}
		switch p.Kind {
		case "text_delta":
			turn.sawTextDelta = true
			if p.Delta == "" {
				return nil
			}
			return s.zcodeAppendStream(turn, "text", p.Delta, now)
		case "reasoning_delta":
			if p.Delta == "" {
				return nil
			}
			return s.zcodeAppendStream(turn, "thinking", p.Delta, now)
		case "tool_call":
			// The full tool invocation in one payload (name + input);
			// tool_input_start/delta/end are its streaming build-up.
			name := p.ToolName
			if name == "" {
				name = "tool"
			}
			detail := string(p.Input)
			if detail == "" && p.Delta != "" {
				detail = p.Delta
			}
			out := s.zcodeFlushStreams(turn, now)
			out = append(out, &ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
				Time: now, Level: "tool", Source: "zcode", ToolName: name, Message: detail,
			}}, Activity: &Activity{Kind: ActivityTool}})
			return out
		}
		return nil

	case "tool.updated":
		// Tool lifecycle (probed live): kind=scheduled/started/result/batch.
		// The call itself is already logged from model.streaming tool_call;
		// only the result carries new information.
		var p struct {
			Kind     string `json:"kind"`
			ToolName string `json:"toolName"`
			Result   *struct {
				Success bool   `json:"success"`
				Content string `json:"content"`
				Error   string `json:"error"`
			} `json:"result"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil || p.Kind != "result" || p.Result == nil {
			return nil
		}
		msg := p.Result.Content
		level := "tool_result"
		if !p.Result.Success {
			level = "error"
			if p.Result.Error != "" {
				msg = p.Result.Error
			}
		}
		name := p.ToolName
		if name == "" {
			name = "tool"
		}
		out := s.zcodeFlushStreams(turn, now)
		out = append(out, &ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
			Time: now, Level: level, Source: "zcode", ToolName: name, Message: msg,
		}}, Activity: &Activity{Kind: ActivityThinking}})
		return out

	case "session.updated":
		// Multipurpose event: the fields present decide the meaning. The
		// assistant-message snapshot carries content + stopReason + usage;
		// other session.updated payloads (model request spans, hooks,
		// pending-input bookkeeping) map to nothing. The snapshot is the
		// authoritative turn text — log it only when the answer did not
		// already stream (else it would duplicate the feed).
		var p struct {
			Content    string      `json:"content"`
			StopReason string      `json:"stopReason"`
			Usage      *zcodeUsage `json:"usage"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil || (p.Content == "" && p.Usage == nil) {
			return nil
		}
		if p.Content != "" {
			s.mu.Lock()
			s.zcodeLastText = p.Content
			s.mu.Unlock()
		}
		var out []*ParsedEvent
		if p.Content != "" && !turn.sawTextDelta {
			out = append(out, &ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
				Time: now, Level: "text", Source: "zcode", Message: p.Content,
			}}, Activity: &Activity{Kind: ActivityWriting}})
		} else {
			out = s.zcodeFlushStreams(turn, now)
		}
		if p.Usage != nil {
			if len(out) > 0 {
				out[len(out)-1].Usage = p.Usage.tokenUsage()
			} else {
				out = append(out, &ParsedEvent{EventType: EventLog, Usage: p.Usage.tokenUsage()})
			}
		}
		return out

	case "turn.completed":
		var p struct {
			Response string      `json:"response"`
			Usage    *zcodeUsage `json:"usage"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Response != "" {
			s.mu.Lock()
			s.zcodeLastText = p.Response
			s.mu.Unlock()
		}
		out := s.zcodeFlushStreams(turn, now)
		fin := s.zcodeFinishEvent("success", s.zcodeTurnText(), turn)
		if p.Usage != nil {
			fin.Usage = p.Usage.tokenUsage()
		}
		return append(out, fin)

	case "turn.failed":
		var p struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
				Detail  string `json:"detail"`
			} `json:"error"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		msg := p.Error.Message
		if msg == "" {
			msg = p.Error.Detail
		}
		if msg == "" {
			msg = p.Error.Code
		}
		if msg == "" {
			msg = "неизвестная ошибка хода"
		}
		out := s.zcodeFlushStreams(turn, now)
		return append(out, s.zcodeFinishEvent("error", msg, turn))
	}
	return nil
}

// zcodeAppendStream adds one streamed delta to its phase buffer, flushing
// the other phase (reasoning↔text switch) and flushing the buffer itself
// at newline boundaries once it grows past zcodeFlushMin.
func (s *Session) zcodeAppendStream(turn *hermesTurn, level, delta string, now time.Time) []*ParsedEvent {
	var out []*ParsedEvent
	if level == "text" && turn.thoughtBuf != "" {
		out = append(out, zcodeBlockEntry(turn, "thinking", now))
	} else if level == "thinking" && turn.textBuf != "" {
		out = append(out, zcodeBlockEntry(turn, "text", now))
	}
	buf := &turn.textBuf
	if level == "text" {
		turn.textBuf += delta
	} else {
		turn.thoughtBuf += delta
		buf = &turn.thoughtBuf
	}
	if len(*buf) >= zcodeFlushMin {
		// Flush up to the last newline so the cut lands on a paragraph
		// boundary the feed rendering already implies.
		if idx := strings.LastIndexByte(*buf, '\n'); idx > 0 {
			emit := (*buf)[:idx]
			*buf = (*buf)[idx+1:]
			out = append(out, &ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
				Time: now, Level: level, Source: "zcode", Message: emit,
			}}})
		}
	}
	activity := ActivityWriting
	if level == "thinking" {
		activity = ActivityThinking
	}
	if len(out) > 0 {
		out[len(out)-1].Activity = &Activity{Kind: activity}
	}
	return out
}

// zcodeBlockEntry emits one whole phase buffer as a single log entry.
func zcodeBlockEntry(turn *hermesTurn, level string, now time.Time) *ParsedEvent {
	msg := turn.textBuf
	if level == "thinking" {
		msg = turn.thoughtBuf
	}
	if level == "thinking" {
		turn.thoughtBuf = ""
	} else {
		turn.textBuf = ""
	}
	return &ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
		Time: now, Level: level, Source: "zcode", Message: msg,
	}}}
}

// zcodeFlushStreams closes both open phase buffers (thought first — it
// precedes the answer), e.g. at a tool call or turn end.
func (s *Session) zcodeFlushStreams(turn *hermesTurn, now time.Time) []*ParsedEvent {
	var out []*ParsedEvent
	if turn.thoughtBuf != "" {
		out = append(out, zcodeBlockEntry(turn, "thinking", now))
	}
	if turn.textBuf != "" {
		out = append(out, zcodeBlockEntry(turn, "text", now))
	}
	return out
}

// zcodeUsage is the provider usage block both the message snapshot and
// turn.completed carry (field names as probed live 2026-11-06).
type zcodeUsage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	ReasoningTokens  int `json:"reasoningTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

func (u *zcodeUsage) tokenUsage() *TokenUsage {
	return &TokenUsage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens + u.ReasoningTokens,
		CacheCreationInputTokens: u.CacheWriteTokens,
		CacheReadInputTokens:     u.CacheReadTokens,
	}
}

// zcodeTurnText is the last assistant message snapshot this turn saw (the
// mapper keeps it on s.zcodeLastText; streamed deltas are feed-only).
func (s *Session) zcodeTurnText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.zcodeLastText
}

func (s *Session) zcodeFinishEvent(stop, text string, turn *hermesTurn) *ParsedEvent {
	// [1113] is the provider's "no balance / no resource package": the
	// zcode_api_key bills a pay-as-you-go wallet, not the Coding Plan
	// subscription (interactive ZCode signs with plan OAuth, not this key).
	if stop == "error" && strings.Contains(text, "[1113]") {
		text += " — у zcode_api_key нет пакета/баланса: нужен API-ключ, привязанный к Coding Plan (https://z.ai/manage-apikey/apikey-list, аккаунт с планом), а не обычный PAYG-ключ"
	}

	// The log entry must not repeat the answer the feed already showed
	// (the GUI renders the result entry as its own block); ResultText
	// still carries the full response for the session/task machinery.
	entryMsg := text
	if stop == "success" && turn.sawTextDelta {
		entryMsg = ""
	}
	entries := []config.LogEntry{{
		Time: time.Now(), Level: "result", Source: "zcode", Message: entryMsg,
	}}
	if stop == "error" {
		turn.failed = text
	}
	ev := &ParsedEvent{
		EventType: EventResult,
		Entries:   entries,
		Result:    &SessionResult{StopReason: stop, ResultText: text},
		Activity:  &Activity{Kind: ActivityIdle},
	}
	if q := questionFromMarker(text); q != nil {
		ev.Questions = []Question{*q}
	}
	return ev
}
