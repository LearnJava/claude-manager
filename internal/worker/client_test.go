package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"claude-manager/internal/config"
)

func noSleep(ctx context.Context, d time.Duration) bool {
	return ctx.Err() == nil
}

func testWorkerConfig(baseURL string) config.WorkerConfig {
	return config.WorkerConfig{
		Name:              "test-worker",
		BaseURL:           baseURL,
		Model:             "test/model",
		KeyEnv:            fmt.Sprintf("TEST_KEY_%p", &baseURL), // unique per test to avoid gate cross-talk
		Role:              "hands",
		ReasoningEffort:   "low",
		MaxOutputTokens:   1000,
		ContinuationCap:   3,
		RequestTimeoutSec: 5,
	}
}

// sseFrame writes one "data: {...}" event for a chat completion chunk.
func sseFrame(w http.ResponseWriter, content string, finish string) {
	chunk := map[string]any{
		"choices": []map[string]any{
			{
				"delta": map[string]any{"content": content},
			},
		},
	}
	if finish != "" {
		chunk["choices"].([]map[string]any)[0]["finish_reason"] = finish
	}
	b, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", b)
	w.(http.Flusher).Flush()
}

func writeDone(w http.ResponseWriter) {
	fmt.Fprint(w, "data: [DONE]\n\n")
	w.(http.Flusher).Flush()
}

// --- basic completion ---

func TestClientCompleteSingleTurn(t *testing.T) {
	var gotBody chatCompletionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Errorf("Authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "hello ", "")
		sseFrame(w, "world", "stop")
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	c := NewClient(cfg, "secret-key")
	c.sleep = noSleep

	res, err := c.Complete(context.Background(), "task-1", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Content != "hello world" {
		t.Errorf("content = %q, want %q", res.Content, "hello world")
	}
	if res.FinishReason != "stop" {
		t.Errorf("finish = %q, want stop", res.FinishReason)
	}
	if res.Continuations != 0 {
		t.Errorf("continuations = %d, want 0", res.Continuations)
	}
	if gotBody.Model != "test/model" || !gotBody.Stream {
		t.Errorf("request body model/stream wrong: %+v", gotBody)
	}
	if gotBody.Reasoning == nil || gotBody.Reasoning.Effort != "low" {
		t.Errorf("reasoning effort not sent: %+v", gotBody.Reasoning)
	}
	if gotBody.MaxTokens != 1000 {
		t.Errorf("max_tokens = %d, want 1000", gotBody.MaxTokens)
	}
}

func TestClientReasoningNoneOmitted(t *testing.T) {
	var gotRaw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotRaw)
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "ok", "stop")
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	cfg.ReasoningEffort = "none"
	c := NewClient(cfg, "key")
	c.sleep = noSleep

	if _, err := c.Complete(context.Background(), "t", []ChatMessage{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := gotRaw["reasoning"]; ok {
		t.Errorf("reasoning field sent despite effort=none: %v", gotRaw["reasoning"])
	}
}

// --- continuation on finish_reason=length ---

func TestClientContinuationOnLength(t *testing.T) {
	var calls int32
	var lastMessages []ChatMessage
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		var body chatCompletionRequest
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		lastMessages = body.Messages
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			sseFrame(w, "part1", "length")
		} else {
			sseFrame(w, "part2", "stop")
		}
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	c := NewClient(cfg, "key")
	c.sleep = noSleep

	res, err := c.Complete(context.Background(), "t", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Content != "part1part2" {
		t.Errorf("content = %q, want part1part2", res.Content)
	}
	if res.FinishReason != "stop" || res.Continuations != 1 {
		t.Errorf("finish=%q continuations=%d, want stop/1", res.FinishReason, res.Continuations)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	// second call must carry the assistant partial + continue prompt
	mu.Lock()
	defer mu.Unlock()
	if len(lastMessages) != 3 {
		t.Fatalf("second request messages = %+v, want 3", lastMessages)
	}
	if lastMessages[1].Role != "assistant" || lastMessages[1].Content != "part1" {
		t.Errorf("assistant partial not appended: %+v", lastMessages[1])
	}
	if lastMessages[2].Role != "user" || lastMessages[2].Content != continuePrompt {
		t.Errorf("continue prompt not appended: %+v", lastMessages[2])
	}
}

func TestClientContinuationCapExceeded(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "x", "length") // always truncated
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	cfg.ContinuationCap = 2
	c := NewClient(cfg, "key")
	c.sleep = noSleep

	res, err := c.Complete(context.Background(), "t", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 { // 1 initial + 2 continuations
		t.Fatalf("calls = %d, want 3 (cap=2)", got)
	}
	if res.Continuations != 2 || res.FinishReason != "length" {
		t.Errorf("continuations=%d finish=%q, want 2/length (still truncated)", res.Continuations, res.FinishReason)
	}
	if res.Content != "xxx" {
		t.Errorf("content = %q, want xxx", res.Content)
	}
}

// --- 403/429 backoff ---

func TestClientRateLimitRetriesThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("slow down"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "ok", "stop")
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	c := NewClient(cfg, "key")
	var slept []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) bool {
		slept = append(slept, d)
		return true
	}

	res, err := c.Complete(context.Background(), "t", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Content != "ok" {
		t.Errorf("content = %q", res.Content)
	}
	if len(slept) != 2 {
		t.Fatalf("backoff sleeps = %d, want 2", len(slept))
	}
	if slept[1] <= slept[0] {
		t.Errorf("backoff not progressive: %v then %v", slept[0], slept[1])
	}
}

func TestClientRateLimitExhausted403(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	c := NewClient(cfg, "key")
	c.sleep = noSleep

	_, err := c.Complete(context.Background(), "t", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected terminal error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("error = %v, want mention of exhausted attempts", err)
	}
	if got := atomic.LoadInt32(&calls); got != maxAttempts {
		t.Errorf("calls = %d, want %d (maxAttempts)", got, maxAttempts)
	}
}

func TestClientNonRetryableStatusFailsImmediately(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad model"))
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	c := NewClient(cfg, "key")
	c.sleep = noSleep

	_, err := c.Complete(context.Background(), "t", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %v, want mention of status 400", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (no retry on non-retryable status)", got)
	}
}

// --- transport error recreates the http.Client ---

type flakyTransport struct {
	failTimes int32
	calls     int32
}

func (t *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if atomic.AddInt32(&t.calls, 1) <= t.failTimes {
		return nil, &net.OpError{Op: "dial", Err: errors.New("simulated connection reset")}
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestClientTransportErrorRecreatesHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "ok", "stop")
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	c := NewClient(cfg, "key")
	c.sleep = noSleep
	c.httpClient = &http.Client{Transport: &flakyTransport{failTimes: 1}, Timeout: 5 * time.Second}

	res, err := c.Complete(context.Background(), "t", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Content != "ok" {
		t.Errorf("content = %q", res.Content)
	}
	if _, stillFlaky := c.currentHTTPClient().Transport.(*flakyTransport); stillFlaky {
		t.Error("http.Client was not recreated after a transport error")
	}
}

// --- context cancellation stops retries immediately ---

func TestClientContextCancelStopsRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	c := NewClient(cfg, "key")
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(ctx context.Context, d time.Duration) bool {
		cancel() // cancel during the first backoff wait
		return false
	}

	_, err := c.Complete(ctx, "t", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (stopped after cancel)", got)
	}
}

// --- per-key_env semaphore serializes concurrent requests ---

func TestKeyGateSerializesRequestsPerKey(t *testing.T) {
	var current, maxConcurrent int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&current, 1)
		for {
			m := atomic.LoadInt32(&maxConcurrent)
			if n <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&current, -1)
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "ok", "stop")
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	cfg.KeyEnv = "SHARED_KEY_ENV_TEST"

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := NewClient(cfg, "key")
			c.sleep = noSleep
			if _, err := c.Complete(context.Background(), "t", []ChatMessage{{Role: "user", Content: "hi"}}, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxConcurrent); got != 1 {
		t.Errorf("max concurrent requests for shared key = %d, want 1", got)
	}
}

func TestResolveAPIKey(t *testing.T) {
	cfg := config.WorkerConfig{Name: "w", KeyEnv: "CM_TEST_WORKER_KEY_UNSET_XYZ"}
	if _, err := ResolveAPIKey(cfg); err == nil {
		t.Error("unset env var: expected error")
	}

	t.Setenv("CM_TEST_WORKER_KEY_SET_XYZ", "abc123")
	cfg.KeyEnv = "CM_TEST_WORKER_KEY_SET_XYZ"
	key, err := ResolveAPIKey(cfg)
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if key != "abc123" {
		t.Errorf("key = %q, want abc123", key)
	}
}
