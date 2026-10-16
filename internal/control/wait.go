package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"claude-manager/internal/session"
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

// Wait timeouts: a request without timeout_ms waits DefaultWaitTimeout, and
// no request waits longer than MaxWaitTimeout (a longer ask is clamped, never
// refused: the caller just calls again).
const (
	DefaultWaitTimeout = 5 * time.Second
	MaxWaitTimeout     = 10 * time.Minute
)

// attentionStatuses are the session statuses that mean "a human is needed".
var attentionStatuses = map[string]bool{
	"waiting_permission": true, "waiting_for_user": true, "error": true,
}

// attentionEvents are the events (all keyed by "id" = session id) that a
// wait_for_any caller wants: a question, a permission, an error, a finished
// task or turn.
var attentionEvents = map[string]bool{
	session.EventNameQuestion: true, session.EventNamePermission: true,
	session.EventNameError: true, session.EventNameTaskDone: true,
	session.EventNameResult: true,
}

// waitRequest is the body of POST /wait.
type waitRequest struct {
	Event     string                     `json:"event"`      // event name filter (required unless attention is set)
	Match     map[string]json.RawMessage `json:"match"`      // field equality constraints
	Attention []string                   `json:"attention"`  // wait_for_any: session ids; the first "needs a human" event wins
	TimeoutMs int64                      `json:"timeout_ms"` // 0 → DefaultWaitTimeout; capped at MaxWaitTimeout
	// SinceSeq limits the ring buffer to events numbered above it (an action's
	// returned seq). Unset: wait_for_status reads the current status instead of
	// the buffer (StatusNow), and wait_for_event may return a past event.
	SinceSeq  *uint64 `json:"since_seq,omitempty"`
	StatusNow bool    `json:"status_now,omitempty"` // wait_for_status: the current status satisfies the wait
}

// waitResponse is the body of the /wait reply on success.
type waitResponse struct {
	Reached bool            `json:"reached"`
	Event   string          `json:"event"`
	Data    json.RawMessage `json:"data"`
	TS      time.Time       `json:"ts"`
	Seq     uint64          `json:"seq"` // the event's number, or the emitter's current one for a state read
}

// waitTimeoutResponse is the 408 body: the wait ran out, nothing is wrong.
type waitTimeoutResponse struct {
	Error         string            `json:"error"`
	Reached       bool              `json:"reached"`
	TimeoutMs     int64             `json:"timeout_ms"`
	CurrentStatus string            `json:"current_status,omitempty"`
	Statuses      map[string]string `json:"statuses,omitempty"`
}

// handleWait blocks until the first matching event arrives (checking the ring
// buffer first) or the timeout expires. It also returns when the client goes
// away, releasing its event subscription: a cancelled MCP call must not leave
// a waiter behind.
//
//	POST /wait  {"event":"session:status","match":{"id":"p/s","status":"working"},"timeout_ms":5000}
//	POST /wait  {"attention":["p/s1","p/s2"],"timeout_ms":60000}
//
// Response on match: 200 + waitResponse JSON.
// Response on timeout: 408 + waitTimeoutResponse (reached=false, current status).
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
	if req.Event == "" && len(req.Attention) == 0 {
		http.Error(w, "event field is required", http.StatusBadRequest)
		return
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	if timeout > MaxWaitTimeout {
		timeout = MaxWaitTimeout
	}

	var matches func(EnvelopedEvent) bool
	if len(req.Attention) > 0 {
		// Only the future counts: a replayed question may long be answered.
		// What is pending right now is read from the manager instead.
		if env, ok := s.attentionNow(req.Attention); ok {
			s.writeWaitResponse(w, env)
			return
		}
		matches = func(env EnvelopedEvent) bool { return attentionMatch(env, req.Attention) }
	} else {
		matches = func(env EnvelopedEvent) bool {
			return env.Event == req.Event && matchEnvelope(env, req.Match)
		}
	}

	// Subscribe before reading any state so nothing slips between the read and
	// the wait. Only events the waiter can accept enter its channel.
	ch, unsub := s.emitter.SubscribeFiltered(matches)
	defer unsub()

	if len(req.Attention) == 0 {
		if req.SinceSeq != nil {
			// Only events caused after the caller's action.
			past := s.emitter.Recent(req.Event, 0)
			for i := len(past) - 1; i >= 0 && past[i].Seq > *req.SinceSeq; i-- {
				if matches(past[i]) {
					s.writeWaitResponse(w, past[i])
					return
				}
			}
		} else if req.StatusNow {
			if env, ok := s.statusNow(req.Match); ok {
				s.writeWaitResponse(w, env)
				return
			}
		} else {
			past := s.emitter.Recent(req.Event, 0)
			for i := len(past) - 1; i >= 0; i-- {
				if matches(past[i]) {
					s.writeWaitResponse(w, past[i])
					return
				}
			}
		}
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			s.writeWaitTimeout(w, req, timeout)
			return
		case env, ok := <-ch:
			if !ok {
				return
			}
			if matches(env) {
				s.writeWaitResponse(w, env)
				return
			}
		}
	}
}

// writeWaitTimeout answers 408 with the state the caller needs to decide what
// to do next: the status of the session(s) it waited on.
func (s *Server) writeWaitTimeout(w http.ResponseWriter, req waitRequest, timeout time.Duration) {
	out := waitTimeoutResponse{Error: "timeout", TimeoutMs: timeout.Milliseconds()}
	ids := req.Attention
	if len(ids) == 0 {
		var id string
		if raw, ok := req.Match["id"]; ok && json.Unmarshal(raw, &id) == nil && id != "" {
			ids = []string{id}
		}
	}
	for _, id := range ids {
		if st, ok := s.manager.GetSession(id); ok {
			if out.Statuses == nil {
				out.Statuses = map[string]string{}
			}
			out.Statuses[id] = st.Status
			out.CurrentStatus = st.Status
		}
	}
	if len(ids) != 1 {
		out.CurrentStatus = ""
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusRequestTimeout)
	_ = json.NewEncoder(w).Encode(out)
}

// attentionNow reports a session in ids that needs a human right now: a
// pending question, a pending permission or a blocked status.
func (s *Server) attentionNow(ids []string) (EnvelopedEvent, bool) {
	in := make(map[string]bool, len(ids))
	for _, id := range ids {
		in[id] = true
	}
	mk := func(event string, data any) (EnvelopedEvent, bool) {
		raw, _ := json.Marshal(data)
		return EnvelopedEvent{Event: event, Data: raw, TS: time.Now()}, true
	}
	for _, q := range s.manager.GetPendingQuestions() {
		if in[q.SessionID] {
			return mk(session.EventNameQuestion, session.QuestionEvent{ID: q.SessionID, Question: q.Question})
		}
	}
	for _, p := range s.manager.GetPendingPermissions() {
		if in[p.SessionID] {
			return mk(session.EventNamePermission, session.PermissionEvent{ID: p.SessionID, Request: p})
		}
	}
	for _, id := range ids {
		if st, ok := s.manager.GetSession(id); ok && attentionStatuses[st.Status] {
			return mk(session.EventNameStatus, session.StatusEvent{ID: id, Status: st.Status})
		}
	}
	return EnvelopedEvent{}, false
}

// attentionMatch reports whether env is a "needs a human" event of a session in ids.
func attentionMatch(env EnvelopedEvent, ids []string) bool {
	isStatus := env.Event == session.EventNameStatus
	if !isStatus && !attentionEvents[env.Event] {
		return false
	}
	var d struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if json.Unmarshal(env.Data, &d) != nil {
		return false
	}
	if isStatus && !attentionStatuses[d.Status] {
		return false
	}
	for _, id := range ids {
		if id == d.ID {
			return true
		}
	}
	return false
}

// statusNow answers a wait_for_status whose target the session already has:
// a synthesised session:status event for the current state.
func (s *Server) statusNow(match map[string]json.RawMessage) (EnvelopedEvent, bool) {
	var id, status string
	if json.Unmarshal(match["id"], &id) != nil || json.Unmarshal(match["status"], &status) != nil || id == "" {
		return EnvelopedEvent{}, false
	}
	st, ok := s.manager.GetSession(id)
	if !ok || st.Status != status {
		return EnvelopedEvent{}, false
	}
	raw, _ := json.Marshal(session.StatusEvent{ID: id, Status: status})
	return EnvelopedEvent{Event: session.EventNameStatus, Data: raw, TS: time.Now()}, true
}

// writeWaitResponse answers 200. An event synthesised from state carries no
// Seq; it gets the emitter's current one, so the caller can chain a wait.
func (s *Server) writeWaitResponse(w http.ResponseWriter, env EnvelopedEvent) {
	if env.Seq == 0 {
		env.Seq = s.emitter.Seq()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(waitResponse{
		Reached: true,
		Event:   env.Event,
		Data:    env.Data,
		TS:      env.TS,
		Seq:     env.Seq,
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
