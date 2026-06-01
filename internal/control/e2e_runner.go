package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// E2EScenario is loaded from testdata/e2e/*.json.
// It pairs RPC actions, event waits, and assertions into an ordered list of
// steps, all of which run against a live control-plane via /rpc and /wait.
type E2EScenario struct {
	Name          string    `json:"name"`
	FakeclaudeDir string    `json:"fakeclaude_dir"` // relative path to fakeclaude scenarios dir
	Config        string    `json:"config"`         // relative path to TOML config
	Steps         []E2EStep `json:"steps"`
}

// E2EStep is one step in an E2EScenario. Exactly one of Do/Wait/Assert must
// be non-empty.
type E2EStep struct {
	// do: fire an RPC action and expect no error.
	Do   string          `json:"do,omitempty"`
	With json.RawMessage `json:"with,omitempty"`

	// wait: block until a matching event arrives or timeout_ms elapses.
	Wait      string                     `json:"wait,omitempty"`
	Match     map[string]json.RawMessage `json:"match,omitempty"`
	TimeoutMs int64                      `json:"timeout_ms,omitempty"`

	// assert: call an RPC query and verify every key in expect against the result.
	Assert string                     `json:"assert,omitempty"`
	Expect map[string]json.RawMessage `json:"expect,omitempty"`
}

// LoadE2EScenario loads a single scenario from a JSON file.
func LoadE2EScenario(path string) (*E2EScenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s E2EScenario
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// LoadE2EScenariosDir loads all *.json files in dir, sorted by filename.
func LoadE2EScenariosDir(dir string) ([]*E2EScenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*E2EScenario
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		s, err := LoadE2EScenario(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// E2ERunner executes an E2EScenario step-by-step against a running
// control-plane. It communicates via /rpc (JSON-RPC 2.0) and /wait (blocking
// event endpoint).
type E2ERunner struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewE2ERunner returns a runner pointed at baseURL authenticated with token.
func NewE2ERunner(baseURL, token string) *E2ERunner {
	return &E2ERunner{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Run executes all steps in scenario in order and returns the first error.
func (r *E2ERunner) Run(scenario *E2EScenario) error {
	for i := range scenario.Steps {
		if err := r.execStep(i, &scenario.Steps[i]); err != nil {
			return fmt.Errorf("step[%d]: %w", i, err)
		}
	}
	return nil
}

func (r *E2ERunner) execStep(i int, step *E2EStep) error {
	switch {
	case step.Do != "":
		return r.execDo(step)
	case step.Wait != "":
		return r.execWait(step)
	case step.Assert != "":
		return r.execAssert(step)
	default:
		return fmt.Errorf("step[%d] has no do/wait/assert", i)
	}
}

// execDo fires an RPC action and checks there is no RPC-level error.
func (r *E2ERunner) execDo(step *E2EStep) error {
	params := step.With
	if params == nil {
		params = json.RawMessage(`{}`)
	}
	resp, err := r.rpc(context.Background(), step.Do, params)
	if err != nil {
		return fmt.Errorf("do %s: %w", step.Do, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("do %s: rpc error %d: %s", step.Do, resp.Error.Code, resp.Error.Message)
	}
	return nil
}

// execWait POSTs to /wait and blocks until the matching event arrives or the
// timeout elapses. The HTTP client deadline is set to timeout_ms + 5 s.
func (r *E2ERunner) execWait(step *E2EStep) error {
	timeout := step.TimeoutMs
	if timeout <= 0 {
		timeout = 10000
	}
	// Give the HTTP layer slightly more time than the /wait handler needs.
	clientDeadline := time.Duration(timeout+5000) * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), clientDeadline)
	defer cancel()

	wr := waitRequest{
		Event:     step.Wait,
		Match:     step.Match,
		TimeoutMs: timeout,
	}
	body, _ := json.Marshal(wr)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/wait", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("wait %s: build request: %w", step.Wait, err)
	}
	req.Header.Set("X-CM-Token", r.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("wait %s: %w", step.Wait, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestTimeout {
		matchJSON, _ := json.Marshal(step.Match)
		return fmt.Errorf("wait %s: timed out after %dms (match=%s)", step.Wait, timeout, matchJSON)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("wait %s: http %d: %s", step.Wait, resp.StatusCode, b)
	}
	return nil
}

// execAssert calls an RPC query and verifies that each key in Expect matches
// the corresponding value in the result. Values are compared by re-marshalling
// through json.Unmarshal/json.Marshal to normalise float64/int differences.
func (r *E2ERunner) execAssert(step *E2EStep) error {
	params := step.With
	if params == nil {
		params = json.RawMessage(`{}`)
	}
	resp, err := r.rpc(context.Background(), step.Assert, params)
	if err != nil {
		return fmt.Errorf("assert %s: %w", step.Assert, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("assert %s: rpc error %d: %s", step.Assert, resp.Error.Code, resp.Error.Message)
	}
	if len(step.Expect) == 0 {
		return nil
	}
	if resp.Result == nil {
		return fmt.Errorf("assert %s: result is nil but expect has %d entries", step.Assert, len(step.Expect))
	}

	// Re-encode the result to get stable JSON, then build a key→raw-JSON map.
	resultJSON, err := json.Marshal(resp.Result)
	if err != nil {
		return fmt.Errorf("assert %s: marshal result: %w", step.Assert, err)
	}
	var resultMap map[string]json.RawMessage
	if err := json.Unmarshal(resultJSON, &resultMap); err != nil {
		return fmt.Errorf("assert %s: result is not a JSON object: %s", step.Assert, resultJSON)
	}

	for key, expectedRaw := range step.Expect {
		actualRaw, ok := resultMap[key]
		if !ok {
			return fmt.Errorf("assert %s: key %q not in result", step.Assert, key)
		}
		// Normalise both sides through any so that "1.5" and "1.50" compare equal
		// and integer/float differences from JSON round-trips are absorbed.
		var ev, av any
		_ = json.Unmarshal(expectedRaw, &ev)
		_ = json.Unmarshal(actualRaw, &av)
		eb, _ := json.Marshal(ev)
		ab, _ := json.Marshal(av)
		if !bytes.Equal(eb, ab) {
			return fmt.Errorf("assert %s: key %q: want %s, got %s", step.Assert, key, eb, ab)
		}
	}
	return nil
}

// rpc sends a single JSON-RPC 2.0 request to /rpc and returns the response.
func (r *E2ERunner) rpc(ctx context.Context, method string, params json.RawMessage) (*rpcResponse, error) {
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/rpc", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("X-CM-Token", r.token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http post /rpc: %w", err)
	}
	defer resp.Body.Close()

	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode rpc response: %w", err)
	}
	return &out, nil
}
