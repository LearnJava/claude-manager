package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// eofDrainTimeout is how long cm-mcp waits for in-flight requests after the
// client closed stdin, before it exits.
const eofDrainTimeout = 5 * time.Second

// stdioGate sits between the process's stdin/stdout and the SDK transport.
//
// The SDK ends the whole session on the first malformed line (invalid JSON, a
// non-object, a batch it cannot route). The spec asks for a JSON-RPC error and
// carrying on, so the gate reads stdin line by line, answers a bad line itself
// and forwards only well-formed messages. It is also the writer the SDK
// sends responses to, which lets it:
//   - serialise its own error lines with the SDK's messages (one mutex);
//   - drop the response to a request the client cancelled
//     (notifications/cancelled: the receiver SHOULD NOT answer it);
//   - on EOF, hold the pipe open until the answered requests are written.
type stdioGate struct {
	out io.Writer

	mu        sync.Mutex
	pending   map[string]bool // request ids read and not yet answered; true = cancelled
	idle      chan struct{}   // closed and replaced whenever pending shrinks
	drainWait time.Duration
}

func newStdioGate(out io.Writer) *stdioGate {
	return &stdioGate{out: out, pending: map[string]bool{}, idle: make(chan struct{}), drainWait: eofDrainTimeout}
}

// idKey is a canonical map key for a JSON-RPC id (1 and 1.0 are the same id).
func idKey(raw json.RawMessage) (string, bool) {
	var v any
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return "", false
	}
	b, _ := json.Marshal(v)
	return string(b), true
}

// writeErrorLocked writes one error response. id nil means "null".
func (g *stdioGate) writeError(id json.RawMessage, code int64, msg string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	line, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": msg},
	})
	g.mu.Lock()
	defer g.mu.Unlock()
	_, _ = g.out.Write(append(line, '\n'))
}

// Write receives one SDK message (the SDK writes each message in one call).
func (g *stdioGate) Write(p []byte) (int, error) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if json.Unmarshal(p, &m) == nil && m.Method == "" && len(m.ID) > 0 {
		if key, ok := idKey(m.ID); ok {
			cancelled, known := g.pending[key]
			if known {
				delete(g.pending, key)
				close(g.idle)
				g.idle = make(chan struct{})
			}
			if cancelled {
				return len(p), nil
			}
		}
	}
	return g.out.Write(p)
}

func (g *stdioGate) Close() error {
	if c, ok := g.out.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// waitDrained blocks until every forwarded request is answered or the timeout ends.
func (g *stdioGate) waitDrained() {
	deadline := time.NewTimer(g.drainWait)
	defer deadline.Stop()
	for {
		g.mu.Lock()
		n, idle := len(g.pending), g.idle
		g.mu.Unlock()
		if n == 0 {
			return
		}
		select {
		case <-idle:
		case <-deadline.C:
			return
		}
	}
}

// pump copies in to pw, answering what it cannot forward. On EOF it waits for
// the in-flight requests, then closes pw so the SDK session ends.
func (g *stdioGate) pump(in io.Reader, pw *io.PipeWriter) {
	br := bufio.NewReader(in)
	for {
		line, err := br.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			if g.admit(t) {
				if _, werr := pw.Write(append(t, '\n')); werr != nil {
					return
				}
			}
		}
		if err != nil {
			g.waitDrained()
			pw.Close()
			return
		}
	}
}

// admit reports whether the line is a well-formed JSON-RPC 2.0 message to
// forward; otherwise it has already answered it.
func (g *stdioGate) admit(line []byte) bool {
	if !json.Valid(line) {
		g.writeError(nil, jsonrpc.CodeParseError, "parse error: the line is not valid JSON")
		return false
	}
	if line[0] == '[' {
		g.writeError(nil, jsonrpc.CodeInvalidRequest, "invalid request: JSON-RPC batches are not supported, send one message per line")
		return false
	}
	var m struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  struct {
			RequestID json.RawMessage `json:"requestId"`
		} `json:"params"`
	}
	if line[0] != '{' || json.Unmarshal(line, &m) != nil {
		g.writeError(nil, jsonrpc.CodeInvalidRequest, "invalid request: a message must be a JSON object")
		return false
	}
	_, hasID := idKey(m.ID)
	idForError := m.ID
	if !hasID {
		idForError = nil
	}
	if m.JSONRPC != "2.0" {
		g.writeError(idForError, jsonrpc.CodeInvalidRequest, `invalid request: "jsonrpc" must be "2.0"`)
		return false
	}
	if m.Method != "" && len(m.ID) > 0 && !hasID {
		g.writeError(nil, jsonrpc.CodeInvalidRequest, "invalid request: a request id must be a string or a number, not null")
		return false
	}
	if m.Method == "notifications/cancelled" {
		if key, ok := idKey(m.Params.RequestID); ok {
			g.mu.Lock()
			if _, known := g.pending[key]; known {
				g.pending[key] = true
			}
			g.mu.Unlock()
		}
	} else if m.Method != "" && hasID {
		key, _ := idKey(m.ID)
		g.mu.Lock()
		g.pending[key] = false
		g.mu.Unlock()
	}
	return true
}
