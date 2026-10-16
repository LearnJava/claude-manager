package control

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"claude-manager/internal/session"
	"claude-manager/internal/worker"
)

// Emitter broadcasts named events. Structurally identical to session.Emitter;
// they are kept separate to avoid an import cycle (control imports session,
// session must not import control).
type Emitter interface {
	Emit(event string, data any)
}

// EnvelopedEvent is the wire format sent to WebSocket clients.
type EnvelopedEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
	TS    time.Time       `json:"ts"`
	Seq   uint64          `json:"seq,omitempty"` // monotonic emission number, assigned by ControlEmitter
}

// ── WailsEmitter ─────────────────────────────────────────────────────────────

// WailsEmitter wraps runtime.EventsEmit. A Wails context is available only
// after startup, so call SetContext before the first Emit.
type WailsEmitter struct {
	mu  sync.RWMutex
	ctx context.Context
}

func NewWailsEmitter() *WailsEmitter { return &WailsEmitter{} }

// SetContext stores the Wails runtime context; safe to call concurrently.
func (e *WailsEmitter) SetContext(ctx context.Context) {
	e.mu.Lock()
	e.ctx = ctx
	e.mu.Unlock()
}

func (e *WailsEmitter) Emit(event string, data any) {
	e.mu.RLock()
	ctx := e.ctx
	e.mu.RUnlock()
	if ctx == nil {
		return
	}
	wruntime.EventsEmit(ctx, event, data)
}

// ── MultiEmitter ─────────────────────────────────────────────────────────────

// MultiEmitter fans out every Emit to each child in insertion order.
type MultiEmitter struct {
	children []Emitter
}

func NewMultiEmitter(children ...Emitter) *MultiEmitter {
	return &MultiEmitter{children: children}
}

func (m *MultiEmitter) Emit(event string, data any) {
	for _, c := range m.children {
		c.Emit(event, data)
	}
}

// SetContext propagates to any child that supports it (e.g. WailsEmitter).
func (m *MultiEmitter) SetContext(ctx context.Context) {
	type ctxSetter interface{ SetContext(context.Context) }
	for _, c := range m.children {
		if cs, ok := c.(ctxSetter); ok {
			cs.SetContext(ctx)
		}
	}
}

// ── ControlEmitter ────────────────────────────────────────────────────────────

// ControlEmitter serialises events to all connected WebSocket subscribers
// and maintains a bounded ring buffer per event name plus a global ring for
// replay and /wait queries.
type ControlEmitter struct {
	mu         sync.Mutex
	subs       map[chan EnvelopedEvent]struct{}
	ringSize   int
	globalRing []EnvelopedEvent
	perEvent   map[string][]EnvelopedEvent
	seq        uint64
	filters    map[chan EnvelopedEvent]func(EnvelopedEvent) bool
}

func NewControlEmitter(ringSize int) *ControlEmitter {
	if ringSize <= 0 {
		ringSize = 200
	}
	return &ControlEmitter{
		subs:     make(map[chan EnvelopedEvent]struct{}),
		ringSize: ringSize,
		perEvent: make(map[string][]EnvelopedEvent),
		filters:  make(map[chan EnvelopedEvent]func(EnvelopedEvent) bool),
	}
}

// Seq is the number of the last emitted event (0 before the first). An action
// reads it before it acts and passes it as since_seq to a wait, which then
// sees only the events the action caused.
func (c *ControlEmitter) Seq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seq
}

func (c *ControlEmitter) Emit(event string, data any) {
	raw, _ := json.Marshal(data)
	env := EnvelopedEvent{Event: event, Data: raw, TS: time.Now()}

	c.mu.Lock()
	c.seq++
	env.Seq = c.seq
	c.globalRing = appendRing(c.globalRing, env, c.ringSize)
	c.perEvent[event] = appendRing(c.perEvent[event], env, c.ringSize)
	type target struct {
		ch     chan EnvelopedEvent
		accept func(EnvelopedEvent) bool
	}
	chans := make([]target, 0, len(c.subs))
	for ch := range c.subs {
		chans = append(chans, target{ch, c.filters[ch]})
	}
	c.mu.Unlock()

	for _, t := range chans {
		if t.accept != nil && !t.accept(env) {
			continue
		}
		ch := t.ch
		select {
		case ch <- env:
		default: // slow subscriber: drop rather than block
		}
	}
}

// Subscribe returns a buffered channel of future events and a cancel func
// that unsubscribes and closes the channel.
func (c *ControlEmitter) Subscribe() (chan EnvelopedEvent, func()) {
	return c.SubscribeFiltered(nil)
}

// SubscribeFiltered is Subscribe for a waiter that can accept only some
// events: the rest never enter its channel, so a burst of session:log cannot
// push the awaited event out of the 64-slot buffer. A nil accept takes all.
func (c *ControlEmitter) SubscribeFiltered(accept func(EnvelopedEvent) bool) (chan EnvelopedEvent, func()) {
	ch := make(chan EnvelopedEvent, 64)
	c.mu.Lock()
	c.subs[ch] = struct{}{}
	if accept != nil {
		c.filters[ch] = accept
	}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.subs, ch)
		delete(c.filters, ch)
		c.mu.Unlock()
		close(ch)
	}
}

// Recent returns up to n past events from the ring buffer.
// If eventFilter is non-empty, only events with that name are included.
func (c *ControlEmitter) Recent(eventFilter string, n int) []EnvelopedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var src []EnvelopedEvent
	if eventFilter != "" {
		src = c.perEvent[eventFilter]
	} else {
		src = c.globalRing
	}
	if n <= 0 || n > len(src) {
		n = len(src)
	}
	if n == 0 {
		return nil
	}
	out := make([]EnvelopedEvent, n)
	copy(out, src[len(src)-n:])
	return out
}

func appendRing(buf []EnvelopedEvent, ev EnvelopedEvent, max int) []EnvelopedEvent {
	buf = append(buf, ev)
	if len(buf) > max {
		buf = buf[len(buf)-max:]
	}
	return buf
}

// EventNames lists every event name the app emits, built from the constants
// of the emitting packages: it is the enum of wait_for_event and the single
// source for what a subscriber can wait on.
var EventNames = []string{
	session.EventNameStatus, session.EventNameLog, session.EventNameTaskDone,
	session.EventNameRateLimit, session.EventNamePermission, session.EventNameQuestion,
	session.EventNameContext, session.EventNameTodo, session.EventNameActivity,
	session.EventNameTaskSource, session.EventNameInit, session.EventNameResult,
	session.EventNameError, session.EventNameStopReq, session.EventNameQueueChanged,
	session.EventNamePlanStatus, session.EventNameRoadmapProgress,
	session.EventNameBudgetAlert, session.EventNameLoopAlert, session.EventNameSkillsChanged,
	session.EventNameSkillProgress, session.EventNameRegression, session.EventNameImportProgress,
	worker.EventRound, worker.EventPatch, worker.EventGate, worker.EventDone,
}

// SubscriberCount is the number of live Subscribe channels (tests use it to
// prove that a cancelled wait released its subscription).
func (c *ControlEmitter) SubscriberCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subs)
}
