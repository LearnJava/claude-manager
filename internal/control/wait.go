package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	// All requests arrive on loopback and have already passed token auth.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleEvents upgrades the connection to WebSocket and streams every
// session:* event from the ControlEmitter until the client disconnects.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch, unsub := s.emitter.Subscribe()
	defer unsub()

	// Pump events until the client disconnects or the subscription is closed.
	for env := range ch {
		if err := conn.WriteJSON(env); err != nil {
			return
		}
	}
}

// waitRequest is the body of POST /wait.
type waitRequest struct {
	Event     string                     `json:"event"`      // event name filter (required)
	Match     map[string]json.RawMessage `json:"match"`      // field equality constraints
	TimeoutMs int64                      `json:"timeout_ms"` // 0 → 5 000 ms default
}

// waitResponse is the body of the /wait reply on success.
type waitResponse struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
	TS    time.Time       `json:"ts"`
}

// handleWait blocks until the first matching event arrives (checking the ring
// buffer first) or the timeout expires.
//
//	POST /wait  {"event":"session:status","match":{"id":"p/s","status":"working"},"timeout_ms":5000}
//
// Response on match: 200 + waitResponse JSON.
// Response on timeout: 408 + {"error":"timeout"}.
func (s *Server) handleWait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req waitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Event == "" {
		http.Error(w, "event field is required", http.StatusBadRequest)
		return
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	// Check the ring buffer for events that already satisfy the match.
	past := s.emitter.Recent(req.Event, 0)
	for i := len(past) - 1; i >= 0; i-- {
		if matchEnvelope(past[i], req.Match) {
			writeWaitResponse(w, past[i])
			return
		}
	}

	// Subscribe and wait for a future matching event.
	ch, unsub := s.emitter.Subscribe()
	defer unsub()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case <-deadline.C:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestTimeout)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "timeout"})
			return
		case env, ok := <-ch:
			if !ok {
				return
			}
			if env.Event == req.Event && matchEnvelope(env, req.Match) {
				writeWaitResponse(w, env)
				return
			}
		}
	}
}

func writeWaitResponse(w http.ResponseWriter, env EnvelopedEvent) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(waitResponse{
		Event: env.Event,
		Data:  env.Data,
		TS:    env.TS,
	})
}

// matchEnvelope reports whether env.Data contains all key-value pairs in
// match. Values are compared by re-marshalling to normalise float64/int
// differences introduced by JSON round-tripping.
func matchEnvelope(env EnvelopedEvent, match map[string]json.RawMessage) bool {
	if len(match) == 0 {
		return true
	}
	var dataMap map[string]any
	if err := json.Unmarshal(env.Data, &dataMap); err != nil {
		return false
	}
	for k, mv := range match {
		dv, ok := dataMap[k]
		if !ok {
			return false
		}
		// Normalise by unmarshalling then re-marshalling both sides.
		var matchVal any
		if err := json.Unmarshal(mv, &matchVal); err != nil {
			return false
		}
		mb, _ := json.Marshal(matchVal)
		db, _ := json.Marshal(dv)
		if !bytes.Equal(mb, db) {
			return false
		}
	}
	return true
}
