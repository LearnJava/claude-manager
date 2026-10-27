package config

import (
	"fmt"
	"strings"
	"time"
)

// GateSchedule is a parsed `gate_schedule`: a local time of day.
type GateSchedule struct {
	Hour, Minute int
}

// ParseGateSchedule parses "HH:MM" (24-hour, local time). An empty string is an
// error here; callers check for "" first, since empty means "off".
func ParseGateSchedule(s string) (GateSchedule, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return GateSchedule{}, fmt.Errorf("invalid gate_schedule %q (want HH:MM, local time)", s)
	}
	return GateSchedule{Hour: t.Hour(), Minute: t.Minute()}, nil
}

// LastDue is the latest scheduled instant at or before now: today's slot if it
// has passed, otherwise yesterday's.
func (g GateSchedule) LastDue(now time.Time) time.Time {
	slot := time.Date(now.Year(), now.Month(), now.Day(), g.Hour, g.Minute, 0, 0, now.Location())
	if slot.After(now) {
		slot = time.Date(now.Year(), now.Month(), now.Day()-1, g.Hour, g.Minute, 0, 0, now.Location())
	}
	return slot
}
