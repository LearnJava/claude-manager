package optimization

import "sync"

// Snapshot is a point-in-time view of all optimization monitors for one session.
type Snapshot struct {
	// Context utilization: 0..1, fraction of context window consumed.
	ContextUtilization float64
	// ContextMaxTokens is the highest observed total-token count in the session.
	ContextMaxTokens int
	// ContextWindowSize is the model's context window (from system/init).
	ContextWindowSize int

	// CacheEfficiency: cache_read / (input + cache_read + cache_creation), 0..1.
	CacheEfficiency     float64
	CacheReadTokens     int
	CacheCreationTokens int
	InputTokens         int
	OutputTokens        int
	Turns               int

	// LastLoop is non-nil when a loop was detected during the current session.
	LastLoop *LoopDetected
}

// GlobalSnapshot is a point-in-time view across all active sessions.
type GlobalSnapshot struct {
	// CacheEfficiency is the aggregate cache efficiency across all sessions.
	CacheEfficiency     float64
	CacheReadTokens     int
	CacheCreationTokens int
	InputTokens         int
	OutputTokens        int
	Turns               int
}

// Reporter aggregates data from ContextMonitor, CacheTracker, and LoopDetector
// into unified snapshots for the session manager and frontend.
//
// It is safe for concurrent use.
type Reporter struct {
	ctx   *ContextMonitor
	cache *CacheTracker
	loop  *LoopDetector

	mu       sync.Mutex
	lastLoop map[string]*LoopDetected // keyed by sessionID
}

// NewReporter creates a Reporter wrapping the given monitors.
// Any monitor may be nil; the corresponding fields in snapshots will be zero.
func NewReporter(ctx *ContextMonitor, cache *CacheTracker, loop *LoopDetector) *Reporter {
	return &Reporter{
		ctx:      ctx,
		cache:    cache,
		loop:     loop,
		lastLoop: make(map[string]*LoopDetected),
	}
}

// RecordLoop stores the most recent loop detection for sessionID so it shows
// up in subsequent Snapshot calls. Call this when LoopDetector.Observe returns
// a non-nil result.
func (r *Reporter) RecordLoop(sessionID string, d *LoopDetected) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastLoop[sessionID] = d
}

// ClearLoop clears the stored loop detection for sessionID (e.g. after the
// session manager has acted on it).
func (r *Reporter) ClearLoop(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.lastLoop, sessionID)
}

// Snapshot returns the current optimization state for sessionID.
func (r *Reporter) Snapshot(sessionID string) Snapshot {
	var s Snapshot

	if r.ctx != nil {
		s.ContextUtilization = r.ctx.Utilization()
		r.ctx.mu.Lock()
		s.ContextMaxTokens = r.ctx.maxTokens
		s.ContextWindowSize = r.ctx.contextWindow
		r.ctx.mu.Unlock()
	}

	if r.cache != nil {
		cs, ok := r.cache.SessionStats(sessionID)
		if ok {
			s.CacheEfficiency = cs.Efficiency()
			s.CacheReadTokens = cs.CacheReadTokens
			s.CacheCreationTokens = cs.CacheCreationTokens
			s.InputTokens = cs.InputTokens
			s.OutputTokens = cs.OutputTokens
			s.Turns = cs.Turns
		}
	}

	r.mu.Lock()
	s.LastLoop = r.lastLoop[sessionID]
	r.mu.Unlock()

	return s
}

// GlobalReport returns an aggregate snapshot across all sessions.
func (r *Reporter) GlobalReport() GlobalSnapshot {
	var g GlobalSnapshot
	if r.cache == nil {
		return g
	}
	cs := r.cache.Global()
	g.CacheEfficiency = cs.Efficiency()
	g.CacheReadTokens = cs.CacheReadTokens
	g.CacheCreationTokens = cs.CacheCreationTokens
	g.InputTokens = cs.InputTokens
	g.OutputTokens = cs.OutputTokens
	g.Turns = cs.Turns
	return g
}
