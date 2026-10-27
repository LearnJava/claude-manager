package config

import (
	"os"
	"strings"
	"testing"
)

// The nightly gate schedule (WASTE-TASKS.md WS-10) is private-layer: it survives
// a save/load, never lands in the committed config.toml, and an empty schedule
// stays empty (off).
func TestSaveProjectOverlayGateScheduleRoundTrip(t *testing.T) {
	proj := t.TempDir()
	p := ProjectConfig{Name: "demo", Path: proj, GateSchedule: "03:00",
		GateScheduleGates: []string{"go test ./..."}, GateScheduleQueue: "STATUS-P1.md"}
	if err := SaveProjectOverlay(proj, p, nil); err != nil {
		t.Fatalf("SaveProjectOverlay: %v", err)
	}
	shared, err := os.ReadFile(ProjectConfigPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shared), "gate_schedule") {
		t.Errorf("committed config leaks the private gate schedule:\n%s", shared)
	}
	cfg, err := Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	got := cfg.Projects[0]
	if got.GateSchedule != "03:00" || len(got.GateScheduleGates) != 1 || got.GateScheduleQueue != "STATUS-P1.md" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	p.GateSchedule = ""
	if err := SaveProjectOverlay(proj, p, nil); err != nil {
		t.Fatalf("SaveProjectOverlay: %v", err)
	}
	cfg, err = Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.Projects[0].GateSchedule != "" {
		t.Error("schedule still set after saving it empty")
	}
}

func TestValidateRejectsBadGateSchedule(t *testing.T) {
	cfg := &AppConfig{Projects: []ProjectConfig{{Name: "p", Path: "/x", GateSchedule: "midnight"}}}
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "gate_schedule") {
		t.Fatalf("got %v", err)
	}
	cfg.Projects[0].GateSchedule = "03:00"
	if err := validate(cfg); err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}
}
