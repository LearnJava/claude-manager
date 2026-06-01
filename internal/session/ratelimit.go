package session

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// rateLimitTextPattern matches the textual fallbacks emitted by Claude CLI
// when no structured rate_limit_event is available. See PLAN.md section 6.4.
var rateLimitTextPattern = regexp.MustCompile(`(?i)\b(?:hit your limit|rate[ _-]?limit(?:ed)?|usage limit)\b`)

// resetTimePattern extracts a human-readable reset time of the form
// "resets 3:45pm" or "resets at 15:30".
var resetTimePattern = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?(\d{1,2}:\d{2}(?:\s*[ap]m)?)`)

// detectRateLimitText scans a free-form log line for rate-limit signals.
// On a match it returns a synthetic RateLimitInfo with whatever reset time
// could be parsed from the line (0 if none).
func detectRateLimitText(line string) (*RateLimitInfo, bool) {
	if !rateLimitTextPattern.MatchString(line) {
		return nil, false
	}
	info := &RateLimitInfo{
		Status:        "exceeded",
		RateLimitType: "text",
		Utilization:   1.0,
	}
	if m := resetTimePattern.FindStringSubmatch(line); len(m) == 2 {
		if t, ok := parseResetClock(m[1], time.Now()); ok {
			info.ResetsAt = t.Unix()
		}
	}
	return info, true
}

// parseResetClock parses strings like "3:45pm" or "15:30" into the next
// occurrence of that wall-clock time relative to now.
func parseResetClock(raw string, now time.Time) (time.Time, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "")

	layouts := []string{"3:04pm", "15:04"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			candidate := time.Date(now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), 0, 0, now.Location())
			if !candidate.After(now) {
				candidate = candidate.Add(24 * time.Hour)
			}
			return candidate, true
		}
	}
	return time.Time{}, false
}

// onRateLimit records a rate-limit signal on the session and emits the
// corresponding event. The actual wait happens in Run() between runs.
func (s *Session) onRateLimit(info *RateLimitInfo) {
	if info == nil {
		return
	}
	cp := *info

	until := time.Time{}
	if cp.ResetsAt > 0 {
		until = time.Unix(cp.ResetsAt, 0)
	}

	s.mu.Lock()
	s.rateLimitUntil = until
	s.mu.Unlock()

	s.rateLimited.Store(true)
	s.rateLimitInf.Store(&cp)
	s.emit(SessionEvent{Type: EvtRateLimit, RateLimit: &cp})
}

// waitRateLimit blocks until the rate-limit window expires or ctx is
// cancelled. Returns false if ctx was cancelled.
func (s *Session) waitRateLimit(ctx context.Context) bool {
	d := s.rateLimitDelay()
	return s.sleepCtx(ctx, d)
}

// rateLimitDelay returns the duration to wait before the next attempt.
// Prefers a structured resetsAt if present, otherwise falls back to the
// configured rate_limit_pause.
func (s *Session) rateLimitDelay() time.Duration {
	fallback := time.Duration(s.rateLimitPauseSec) * time.Second
	info := s.rateLimitInf.Load()
	if info == nil {
		return fallback
	}
	if info.ResetsAt <= 0 {
		return fallback
	}
	delay := time.Until(time.Unix(info.ResetsAt, 0))
	if delay <= 0 {
		return fallback
	}
	// Add a small safety margin so we don't retry exactly on the boundary.
	return delay + 5*time.Second
}
