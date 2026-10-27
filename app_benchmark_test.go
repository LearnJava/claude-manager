package main

import (
	"strings"
	"testing"

	"claude-manager/internal/config"
	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

func benchApp(t *testing.T, enabled bool) *App {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{Name: "p", Path: t.TempDir()}}}
	cfg.Optimization.Benchmark = enabled
	return &App{store: st, cfg: cfg}
}

func TestBenchmark_OffRunsNothing(t *testing.T) {
	a := benchApp(t, false)
	spec := experience.BenchmarkSpec{Configs: []store.BenchConfig{{Name: "a"}}}
	if _, err := a.RunBenchmark("p", spec); err == nil || !strings.Contains(err.Error(), "off") {
		t.Fatalf("RunBenchmark with the flag off = %v", err)
	}
	if runs, _ := a.store.ListBenchmarkRuns("p"); len(runs) != 0 {
		t.Errorf("%d run(s) stored with the flag off", len(runs))
	}
	// The report stays readable, and empty.
	if rep, err := a.GetBenchmarkReport("p", 0); err != nil || rep.RunID != 0 || len(rep.Configs) != 0 {
		t.Errorf("GetBenchmarkReport = %+v, %v", rep, err)
	}
}

func TestBenchmark_BadRequestIsRefusedAndReleasesTheLock(t *testing.T) {
	a := benchApp(t, true)
	// No reference cases: an empty suite, refused before anything is stored.
	for i := 0; i < 2; i++ {
		_, err := a.RunBenchmark("p", experience.BenchmarkSpec{Configs: []store.BenchConfig{{Name: "a"}}})
		if err == nil || strings.Contains(err.Error(), "already running") {
			t.Fatalf("attempt %d: err = %v, want the suite error and a free lock", i, err)
		}
	}
	if runs, _ := a.store.ListBenchmarkRuns("p"); len(runs) != 0 {
		t.Errorf("a refused request stored %d run(s)", len(runs))
	}
}
