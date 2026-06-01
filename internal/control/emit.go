package control

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
}

func NewControlEmitter(ringSize int) *ControlEmitter {
	if ringSize <= 0 {
		ringSize = 200
	}
	return &ControlEmitter{
		subs:     make(map[chan EnvelopedEvent]struct{}),
		ringSize: ringSize,
		perEvent: make(map[string][]EnvelopedEvent),
	}
}

func (c *ControlEmitter) Emit(event string, data any) {
	raw, _ := json.Marshal(data)
	env := EnvelopedEvent{Event: event, Data: raw, TS: time.Now()}

	c.mu.Lock()
	c.globalRing = appendRing(c.globalRing, env, c.ringSize)
	c.perEvent[event] = appendRing(c.perEvent[event], env, c.ringSize)
	chans := make([]chan EnvelopedEvent, 0, len(c.subs))
	for ch := range c.subs {
		chans = append(chans, ch)
	}
	c.mu.Unlock()

	for _, ch := range chans {
		select {
		case ch <- env:
		default: // slow subscriber: drop rather than block
		}
	}
}

// Subscribe returns a buffered channel of future events and a cancel func
// that unsubscribes and closes the channel.
func (c *ControlEmitter) Subscribe() (chan EnvelopedEvent, func()) {
	ch := make(chan EnvelopedEvent, 64)
	c.mu.Lock()
	c.subs[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.subs, ch)
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
