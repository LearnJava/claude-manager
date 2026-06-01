package optimization

import (
	"sync"

	"claude-manager/internal/config"
)

// ContextEventType classifies the outcome of one observation by ContextMonitor.
type ContextEventType int

const (
	ContextEventNone ContextEventType = iota
	ContextEventWarn
	ContextEventRestart
)

func (t ContextEventType) String() string {
	switch t {
	case ContextEventNone:
		return "none"
	case ContextEventWarn:
		return "warn"
	case ContextEventRestart:
		return "restart"
	default:
		return "unknown"
	}
}

// ContextEvent is emitted by ContextMonitor after each turn.
type ContextEvent struct {
	Type        ContextEventType
	Utilization float64 // 0..1
	TotalTokens int
	MaxTokens   int
	// RestartMode is the configured restart mode at the moment of detection
	// ("resume" | "fresh" | "ask" | "off"). For Type==ContextEventRestart the
	// session manager uses this to decide how to restart.
	RestartMode string
}

// TokenObserver is the minimal subset of session.TokenUsage that the monitor
// needs. Defined locally to avoid a session->optimization import cycle.
type TokenObserver interface {
	Total() int
}

// ContextMonitor watches per-turn token usage and emits warnings / restart
// signals once configured thresholds are crossed. It is safe for concurrent use.
//
// Total tokens for a turn are computed as
//
//	input_tokens + cache_creation_input_tokens + cache_read_input_tokens
//
// which approximates how much context Claude is re-sending each turn (PLAN.md
// section 20.1 — context is fully re-sent every turn).
type ContextMonitor struct {
	cfg           *config.OptimizationSettings
	contextWindow int

	mu        sync.Mutex
	maxTokens int
	warned    bool
	restarted bool
}

// NewContextMonitor returns a monitor initialized with the given settings and
// context window (e.g. 200_000 for Sonnet, sourced from the system/init event).
//
// If contextWindow is non-positive, observations return ContextEventNone — the
// monitor is effectively disabled until SetContextWindow is called.
func NewContextMonitor(cfg *config.OptimizationSettings, contextWindow int) *ContextMonitor {
	return &ContextMonitor{
		cfg:           cfg,
		contextWindow: contextWindow,
	}
}

// SetContextWindow updates the context window. Typically called once after the
// system/init event reveals the model's actual context size.
func (m *ContextMonitor) SetContextWindow(window int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contextWindow = window
}

// Reset clears the warned / restarted flags. Call after a successful restart so
// the next session generation can emit warnings again.
func (m *ContextMonitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxTokens = 0
	m.warned = false
	m.restarted = false
}

// Observe records a turn's token usage and returns a ContextEvent describing
// any threshold crossing.
//
//   - The first time utilization exceeds the warn threshold, a Warn event
//     fires (once).
//   - The first time utilization exceeds the restart threshold, a Restart
//     event fires (once) — unless restart_mode == "off".
//
// Returning ContextEventRestart is advisory; the caller (session manager) is
// responsible for actually performing the restart.
func (m *ContextMonitor) Observe(usage TokenObserver) ContextEvent {
	if usage == nil || m.cfg == nil {
		return ContextEvent{Type: ContextEventNone}
	}

	total := usage.Total()

	m.mu.Lock()
	defer m.mu.Unlock()

	if total > m.maxTokens {
		m.maxTokens = total
	}

	if m.contextWindow <= 0 {
		return ContextEvent{
			Type:        ContextEventNone,
			TotalTokens: total,
			MaxTokens:   m.maxTokens,
		}
	}

	util := float64(total) / float64(m.contextWindow)

	if !m.restarted &&
		m.cfg.ContextRestartThreshold > 0 &&
		util >= m.cfg.ContextRestartThreshold &&
		m.cfg.ContextRestartMode != "off" {
		m.restarted = true
		return ContextEvent{
			Type:        ContextEventRestart,
			Utilization: util,
			TotalTokens: total,
			MaxTokens:   m.maxTokens,
			RestartMode: m.cfg.ContextRestartMode,
		}
	}

	if !m.warned &&
		m.cfg.ContextWarnThreshold > 0 &&
		util >= m.cfg.ContextWarnThreshold {
		m.warned = true
		return ContextEvent{
			Type:        ContextEventWarn,
			Utilization: util,
			TotalTokens: total,
			MaxTokens:   m.maxTokens,
		}
	}

	return ContextEvent{
		Type:        ContextEventNone,
		Utilization: util,
		TotalTokens: total,
		MaxTokens:   m.maxTokens,
	}
}

// Utilization returns the latest known utilization (max-observed totalTokens
// over contextWindow). Returns 0 if the context window has not been set.
func (m *ContextMonitor) Utilization() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.contextWindow <= 0 {
		return 0
	}
	return float64(m.maxTokens) / float64(m.contextWindow)
}

// TokenUsageAdapter adapts the session-level usage shape to TokenObserver
// without creating a session->optimization import cycle. Callers that already
// have raw numbers can pass these directly.
type TokenUsageAdapter struct {
	InputTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// Total returns the size of context being sent for this turn.
func (u TokenUsageAdapter) Total() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}
