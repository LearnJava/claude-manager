package testkit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// WorkerResponse is one scripted reply of the fake worker (MIXED-TASKS.md
// MP-07). Exactly one of two shapes:
//   - Status >= 400: a plain HTTP error response with Body (429-storm,
//     403-throttling scenarios);
//   - otherwise: a 200 SSE chat-completion stream carrying Content and
//     FinishReason, split into ChunkSize-rune deltas.
type WorkerResponse struct {
	Status       int    `json:"status,omitempty"`        // 0 or 200 = SSE success; >=400 = error response
	Body         string `json:"body,omitempty"`          // body of an error response
	Content      string `json:"content,omitempty"`       // assistant text of a success response
	FinishReason string `json:"finish_reason,omitempty"` // "stop" (default) or "length"
	ChunkSize    int    `json:"chunk_size,omitempty"`    // runes per SSE delta; 0 = single delta
	Repeat       int    `json:"repeat,omitempty"`        // serve this step for N consecutive requests (default 1)
}

// WorkerScenario is a deterministic script for FakeWorker, loaded from
// testdata/worker-scenarios/*.json. Requests consume responses in order;
// a request past the last response is answered with 500 so a drifting test
// fails loudly instead of hanging.
type WorkerScenario struct {
	Name      string           `json:"name"`
	Responses []WorkerResponse `json:"responses"`
}

// LoadWorkerScenario loads and validates a scenario JSON file.
func LoadWorkerScenario(path string) (*WorkerScenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc WorkerScenario
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := sc.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &sc, nil
}

func (sc *WorkerScenario) validate() error {
	if sc.Name == "" {
		return fmt.Errorf("worker scenario missing name")
	}
	if len(sc.Responses) == 0 {
		return fmt.Errorf("worker scenario %q has no responses", sc.Name)
	}
	for i, r := range sc.Responses {
		if r.Repeat < 0 || r.ChunkSize < 0 {
			return fmt.Errorf("worker scenario %q response %d: negative repeat/chunk_size", sc.Name, i)
		}
		if r.Status != 0 && r.Status != http.StatusOK && r.Status < 400 {
			return fmt.Errorf("worker scenario %q response %d: status %d (want 0, 200 or >=400)", sc.Name, i, r.Status)
		}
		if fr := r.FinishReason; fr != "" && fr != "stop" && fr != "length" {
			return fmt.Errorf("worker scenario %q response %d: finish_reason %q (want stop|length)", sc.Name, i, fr)
		}
	}
	return nil
}

// WorkerMessage mirrors one chat message of an incoming request. Defined here
// (not imported from internal/worker) so testkit stays dependency-free and
// importable from any package's tests.
type WorkerMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// WorkerRequest is one recorded chat-completions request.
type WorkerRequest struct {
	Model    string          `json:"model"`
	Messages []WorkerMessage `json:"messages"`
}

// FakeWorker is an http.Handler that plays a WorkerScenario: an
// OpenAI-compatible POST /chat/completions endpoint returning scripted
// responses in order. Safe for concurrent use; every received request is
// recorded for assertions.
type FakeWorker struct {
	scenario *WorkerScenario

	mu       sync.Mutex
	idx      int // current response index
	served   int // requests already answered by the current response
	requests []WorkerRequest
}

// NewFakeWorker returns a handler that serves scenario.
func NewFakeWorker(scenario *WorkerScenario) *FakeWorker {
	return &FakeWorker{scenario: scenario}
}

// Requests returns a copy of all requests received so far.
func (f *FakeWorker) Requests() []WorkerRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]WorkerRequest(nil), f.requests...)
}

// next consumes and returns the response for one request, or nil when the
// scenario is exhausted.
func (f *FakeWorker) next() *WorkerResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	for f.idx < len(f.scenario.Responses) {
		r := &f.scenario.Responses[f.idx]
		repeat := r.Repeat
		if repeat <= 0 {
			repeat = 1
		}
		if f.served < repeat {
			f.served++
			return r
		}
		f.idx++
		f.served = 0
	}
	return nil
}

func (f *FakeWorker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.Error(w, "fakeworker: only POST .../chat/completions is supported", http.StatusNotFound)
		return
	}
	var req WorkerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "fakeworker: bad request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()

	resp := f.next()
	if resp == nil {
		http.Error(w, fmt.Sprintf("fakeworker: scenario %q exhausted", f.scenario.Name), http.StatusInternalServerError)
		return
	}
	if resp.Status != 0 && resp.Status != http.StatusOK {
		http.Error(w, resp.Body, resp.Status)
		return
	}
	writeSSE(w, resp)
}

// sseChunk is the OpenAI streaming chunk shape internal/worker.parseSSE reads.
type sseChunk struct {
	Choices []sseChoice `json:"choices"`
}

type sseChoice struct {
	Delta        sseDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

type sseDelta struct {
	Content string `json:"content"`
}

// writeSSE streams resp.Content as "data: {...}" events: content deltas with
// a null finish_reason, then a final empty delta carrying the finish_reason,
// then "data: [DONE]".
func writeSSE(w http.ResponseWriter, resp *WorkerResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, piece := range splitRunes(resp.Content, resp.ChunkSize) {
		writeSSEEvent(w, sseChunk{Choices: []sseChoice{{Delta: sseDelta{Content: piece}}}})
	}
	finish := resp.FinishReason
	if finish == "" {
		finish = "stop"
	}
	writeSSEEvent(w, sseChunk{Choices: []sseChoice{{FinishReason: &finish}}})
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func writeSSEEvent(w http.ResponseWriter, chunk sseChunk) {
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// splitRunes splits s into pieces of at most size runes (never mid-rune).
// size <= 0 returns s whole; empty s returns nothing.
func splitRunes(s string, size int) []string {
	if s == "" {
		return nil
	}
	if size <= 0 {
		return []string{s}
	}
	var out []string
	runes := []rune(s)
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
	}
	return out
}
