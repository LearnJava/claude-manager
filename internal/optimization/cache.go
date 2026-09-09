package optimization

import (
	"context"
	"sync"
	"time"

	"claude-manager/internal/config"
)

// CacheStats accumulates cache-hit accounting for one session or globally.
//
// Efficiency is defined per PLAN.md 20.3.6 as:
//
//	cache_read / (input + cache_read + cache_creation)
//
// Higher means more of the context is being served from cache (cheap) instead
// of fresh input (10x more expensive) or cache writes (25% more expensive).
type CacheStats struct {
	InputTokens         int
	CacheReadTokens     int
	CacheCreationTokens int
	OutputTokens        int
	Turns               int
}

// Efficiency returns the cache-read ratio in [0,1]. Returns 0 when no input
// tokens have been recorded yet.
func (s CacheStats) Efficiency() float64 {
	denom := s.InputTokens + s.CacheReadTokens + s.CacheCreationTokens
	if denom == 0 {
		return 0
	}
	return float64(s.CacheReadTokens) / float64(denom)
}

// CacheTracker records per-session and aggregate cache statistics. Safe for
// concurrent use across session goroutines.
type CacheTracker struct {
	cfg *config.OptimizationSettings

	mu       sync.Mutex
	sessions map[string]*CacheStats
	global   CacheStats
}

// NewCacheTracker returns a tracker bound to the given OptimizationSettings.
// The cfg pointer may be nil; in that case the tracker still records stats but
// none of the behavior dependent on settings (e.g. stagger delay) is active.
func NewCacheTracker(cfg *config.OptimizationSettings) *CacheTracker {
	return &CacheTracker{
		cfg:      cfg,
		sessions: make(map[string]*CacheStats),
	}
}

// Record adds one turn's token usage to both the per-session and global
// counters. sessionID identifies the session (e.g. "lumen/P1").
func (t *CacheTracker) Record(sessionID string, input, cacheRead, cacheCreation, output int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s, ok := t.sessions[sessionID]
	if !ok {
		s = &CacheStats{}
		t.sessions[sessionID] = s
	}
	s.InputTokens += input
	s.CacheReadTokens += cacheRead
	s.CacheCreationTokens += cacheCreation
	s.OutputTokens += output
	s.Turns++

	t.global.InputTokens += input
	t.global.CacheReadTokens += cacheRead
	t.global.CacheCreationTokens += cacheCreation
	t.global.OutputTokens += output
	t.global.Turns++
}

// SessionStats returns a snapshot of one session's accumulated stats. The
// boolean is false if the session has never been recorded.
func (t *CacheTracker) SessionStats(sessionID string) (CacheStats, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.sessions[sessionID]
	if !ok {
		return CacheStats{}, false
	}
	return *s, true
}

// Global returns a snapshot of the aggregate stats across all sessions.
func (t *CacheTracker) Global() CacheStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.global
}

// SessionEfficiency is a convenience wrapper around SessionStats.Efficiency.
// Returns 0 if the session is unknown or has no recorded input.
func (t *CacheTracker) SessionEfficiency(sessionID string) float64 {
	s, ok := t.SessionStats(sessionID)
	if !ok {
		return 0
	}
	return s.Efficiency()
}

// GlobalEfficiency is a convenience wrapper around Global().Efficiency.
func (t *CacheTracker) GlobalEfficiency() float64 {
	return t.Global().Efficiency()
}

// Reset clears all per-session stats (typically called when a project is
// fully stopped). Global stats are preserved.
func (t *CacheTracker) ResetSessions() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessions = make(map[string]*CacheStats)
}

// StartProjectOptimized launches a batch of session-start callbacks staggered
// by SessionStartDelay seconds, allowing the first session to warm the
// shared system-prompt cache before subsequent sessions issue their API calls
// (PLAN.md 20.3.3).
//
// The first start runs immediately. Each subsequent start waits delay seconds
// (or until ctx is canceled). Returns the first non-nil error from any start
// callback, or ctx.Err() if the batch is canceled mid-flight.
//
// If cfg is nil or SessionStartDelay <= 0, all starts run immediately in
// sequence without any sleeps.
func (t *CacheTracker) StartProjectOptimized(ctx context.Context, starts []func() error) error {
	delay := t.startDelay()

	for i, start := range starts {
		if i > 0 && delay > 0 {
			if !sleepWithCancel(ctx, delay) {
				return ctx.Err()
			}
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		if err := start(); err != nil {
			return err
		}
	}
	return nil
}

// CacheAffinityStart pairs one session's start callback with its launch ID.
// StartProjectOptimized's plain []func() error carries no identity for a
// callback, which is fine for staggering alone but not for reporting which
// session actually started in which position once the batch is ordered.
type CacheAffinityStart struct {
	ID    string
	Start func() error
}

// StartProjectOptimizedOrdered is StartProjectOptimized's ID-aware sibling
// (LEARN-TASKS.md LN-14): starts run in the given slice order, staggered by
// the same SessionStartDelay as StartProjectOptimized. The cache-affinity
// ordering itself — group by launch model, then by descending file overlap
// with the previous pick — is computed by the caller via
// experience.OrderByCacheAffinity (internal/experience/affinity.go): this
// package cannot import internal/experience, since internal/store already
// imports this package (for OutcomeProvider, LEARN-TASKS.md LN-13) and
// internal/experience imports internal/store, so the reverse import would
// cycle. With no reordering applied (starts given in plain config order),
// this is byte-for-byte StartProjectOptimized (LEARN-TASKS.md invariant 6).
func (t *CacheTracker) StartProjectOptimizedOrdered(ctx context.Context, starts []CacheAffinityStart) error {
	fns := make([]func() error, len(starts))
	for i, s := range starts {
		fns[i] = s.Start
	}
	return t.StartProjectOptimized(ctx, fns)
}

// startDelay returns the configured per-session stagger delay.
func (t *CacheTracker) startDelay() time.Duration {
	if t.cfg == nil || t.cfg.SessionStartDelay <= 0 {
		return 0
	}
	return time.Duration(t.cfg.SessionStartDelay) * time.Second
}

// sleepWithCancel sleeps for d but returns false if ctx is canceled first.
// A nil ctx blocks for the full duration.
func sleepWithCancel(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	if ctx == nil {
		time.Sleep(d)
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
