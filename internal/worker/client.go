package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/config"
)

// ChatMessage is one turn in an OpenAI-compatible chat completion request.
type ChatMessage struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// Result is the outcome of Complete: the assistant's full reply, reassembled
// across any finish_reason=length continuations.
type Result struct {
	Content       string // concatenated content across all continuation chunks
	FinishReason  string // finish_reason of the last chunk received ("stop", or "length" if the cap was hit)
	Continuations int    // number of finish_reason=length follow-up requests sent
}

const continuePrompt = "continue exactly where you stopped"

// maxAttempts bounds consecutive retryable failures (403/429 or transport
// errors) for a single exchange before it becomes a terminal error.
const maxAttempts = 5

// ResolveAPIKey reads cfg.KeyEnv from the environment. API keys are never
// stored in config (see validateWorker) — a worker with no key set must fail
// fast rather than send an unauthenticated request.
func ResolveAPIKey(cfg config.WorkerConfig) (string, error) {
	key := os.Getenv(cfg.KeyEnv)
	if key == "" {
		return "", fmt.Errorf("worker %s: environment variable %s is not set", cfg.Name, cfg.KeyEnv)
	}
	return key, nil
}

// Client talks to one OpenAI-compatible worker endpoint over net/http; no SDK.
type Client struct {
	cfg    config.WorkerConfig
	apiKey string

	mu         sync.Mutex
	httpClient *http.Client

	// sleep is overridden in tests to avoid real waits during backoff.
	sleep func(ctx context.Context, d time.Duration) bool
}

// NewClient builds a Client for cfg, authenticating with apiKey (see
// ResolveAPIKey).
func NewClient(cfg config.WorkerConfig, apiKey string) *Client {
	return &Client{
		cfg:        cfg,
		apiKey:     apiKey,
		httpClient: newHTTPClient(cfg.RequestTimeoutSec),
		sleep:      ctxSleep,
	}
}

func newHTTPClient(timeoutSec int) *http.Client {
	timeout := time.Duration(timeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func ctxSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// keyGate returns the process-wide mutex for keyEnv: free-tier throttling is
// shared per API key, so at most one request per key_env may be in flight at
// a time, across every Client built for that key.
var (
	keyGatesMu sync.Mutex
	keyGates   = map[string]*sync.Mutex{}
)

func keyGate(keyEnv string) *sync.Mutex {
	keyGatesMu.Lock()
	defer keyGatesMu.Unlock()
	g, ok := keyGates[keyEnv]
	if !ok {
		g = &sync.Mutex{}
		keyGates[keyEnv] = g
	}
	return g
}

// Complete sends messages and returns the assistant's full reply. When the
// worker stops mid-output (finish_reason=length), it automatically follows up
// with continuePrompt until the reply is complete or cfg.ContinuationCap is
// reached. If store is non-nil, the growing dialogue is persisted after every
// exchange (request/response pair) — the API is stateless, so a crash mid-task
// is recovered by reloading and replaying these messages, not by reconnecting.
func (c *Client) Complete(ctx context.Context, id string, messages []ChatMessage, store *Store) (*Result, error) {
	cap := c.cfg.ContinuationCap
	if cap <= 0 {
		cap = 3
	}

	msgs := append([]ChatMessage(nil), messages...)
	var full strings.Builder
	finish := ""
	continuations := 0

	for {
		content, fr, err := c.doRequest(ctx, msgs)
		if err != nil {
			return nil, err
		}
		full.WriteString(content)
		finish = fr
		msgs = append(msgs, ChatMessage{Role: "assistant", Content: content})
		if err := c.persist(store, id, msgs); err != nil {
			return nil, err
		}

		if finish != "length" || continuations >= cap {
			break
		}
		continuations++
		msgs = append(msgs, ChatMessage{Role: "user", Content: continuePrompt})
		if err := c.persist(store, id, msgs); err != nil {
			return nil, err
		}
	}

	return &Result{Content: full.String(), FinishReason: finish, Continuations: continuations}, nil
}

func (c *Client) persist(store *Store, id string, msgs []ChatMessage) error {
	if store == nil {
		return nil
	}
	if err := store.Save(id, msgs); err != nil {
		return fmt.Errorf("worker %s: persist dialogue: %w", c.cfg.Name, err)
	}
	return nil
}

// doRequest performs one exchange, retrying on 403/429 (progressive backoff —
// throttling, not a revoked key) and on transport errors (recreating the
// http.Client — a poisoned keepalive connection surfaces as repeated TLS/SSL
// failures otherwise). maxAttempts bounds consecutive failures; beyond that
// the round is a terminal error, not another wait.
func (c *Client) doRequest(ctx context.Context, messages []ChatMessage) (string, string, error) {
	gate := keyGate(c.cfg.KeyEnv)
	gate.Lock()
	defer gate.Unlock()

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}

		content, finish, err := c.streamOnce(ctx, messages)
		if err == nil {
			return content, finish, nil
		}
		lastErr = err

		var rle *rateLimitError
		retryable := errors.As(err, &rle)
		if !retryable && isTransportError(err) {
			retryable = true
			c.recreateHTTPClient()
		}
		if !retryable {
			return "", "", fmt.Errorf("worker %s: %w", c.cfg.Name, err)
		}
		if attempt == maxAttempts-1 {
			break
		}
		if !c.sleep(ctx, backoffDelay(attempt)) {
			return "", "", ctx.Err()
		}
	}
	return "", "", fmt.Errorf("worker %s: exhausted %d attempts: %w", c.cfg.Name, maxAttempts, lastErr)
}

// backoffDelay grows exponentially from a 500ms base, capped at 30s, with up
// to one base-delay of jitter to avoid a retry thundering herd.
func backoffDelay(attempt int) time.Duration {
	const base = 500 * time.Millisecond
	const max = 30 * time.Second
	d := base * time.Duration(math.Pow(2, float64(attempt)))
	if d > max {
		d = max
	}
	return d + time.Duration(rand.Int63n(int64(base)))
}

func (c *Client) recreateHTTPClient() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.httpClient = newHTTPClient(c.cfg.RequestTimeoutSec)
}

func (c *Client) currentHTTPClient() *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.httpClient
}

// rateLimitError marks a 403/429 response as retryable-with-backoff.
type rateLimitError struct {
	status int
	body   string
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("rate limited (status %d): %s", e.status, e.body)
}

// isTransportError reports whether err came from the network/transport layer
// rather than an application-level response — the signal to recreate the
// http.Client rather than just retrying on the same connection.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

type chatCompletionRequest struct {
	Model     string           `json:"model"`
	Messages  []ChatMessage    `json:"messages"`
	Stream    bool             `json:"stream"`
	MaxTokens int              `json:"max_tokens,omitempty"`
	Reasoning *reasoningEffort `json:"reasoning,omitempty"`
}

type reasoningEffort struct {
	Effort string `json:"effort"`
}

type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// streamOnce performs a single HTTP exchange: build the request body via
// json.Marshal (UTF-8 is handled by the encoder — the body is never built by
// string/shell interpolation), POST it, and parse the SSE response stream.
func (c *Client) streamOnce(ctx context.Context, messages []ChatMessage) (string, string, error) {
	body := chatCompletionRequest{
		Model:    c.cfg.Model,
		Messages: messages,
		Stream:   true,
	}
	if c.cfg.MaxOutputTokens > 0 {
		body.MaxTokens = c.cfg.MaxOutputTokens
	}
	if eff := c.cfg.ReasoningEffort; eff != "" && eff != "none" {
		body.Reasoning = &reasoningEffort{Effort: eff}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", "", fmt.Errorf("worker %s: marshal request: %w", c.cfg.Name, err)
	}

	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", "", fmt.Errorf("worker %s: build request: %w", c.cfg.Name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.currentHTTPClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", "", &rateLimitError{status: resp.StatusCode, body: string(b)}
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", "", fmt.Errorf("worker %s: unexpected status %d: %s", c.cfg.Name, resp.StatusCode, string(b))
	}

	return parseSSE(resp.Body, c.cfg.Name)
}

// parseSSE reads an OpenAI-style "data: {...}" event stream terminated by
// "data: [DONE]" and reassembles the delta content plus the final finish_reason.
func parseSSE(body io.Reader, workerName string) (string, string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var content strings.Builder
	finish := ""
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return "", "", fmt.Errorf("worker %s: decode stream chunk: %w", workerName, err)
		}
		for _, choice := range chunk.Choices {
			content.WriteString(choice.Delta.Content)
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = *choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("worker %s: read stream: %w", workerName, err)
	}
	if finish == "" {
		finish = "stop"
	}
	return content.String(), finish, nil
}
