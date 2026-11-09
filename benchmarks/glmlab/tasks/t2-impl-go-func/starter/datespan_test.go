package datespan

import (
	"testing"
	"time"
)

func TestParseISORangeBasic(t *testing.T) {
	s, e, err := ParseISORange("2026-01-01..2026-01-05")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); !s.Equal(want) {
		t.Fatalf("start = %v, want %v", s, want)
	}
	if want := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC); !e.Equal(want) {
		t.Fatalf("end = %v, want %v", e, want)
	}
}
