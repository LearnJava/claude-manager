package main

import (
	"testing"

	"claude-manager/internal/config"
)

func TestNightlyJobs(t *testing.T) {
	sess := []config.SessionConfig{{Name: "Developer 1", TaskSource: "STATUS-P1.md"}}
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{
		{Name: "off", Path: "/a", Sessions: sess, Gates: []string{"go test ./..."}},
		{Name: "on", Path: "/b", Sessions: sess, Gates: []string{"go test ./..."}, GateSchedule: "03:00"},
		{Name: "own-gates", Path: "/c", Sessions: sess, Gates: []string{"x"}, GateSchedule: "04:15",
			GateScheduleGates: []string{"go vet ./..."}, GateScheduleQueue: "NIGHT.md"},
		{Name: "no-gates", Path: "/d", Sessions: sess, GateSchedule: "03:00"},
		{Name: "no-queue", Path: "/e", Gates: []string{"go test ./..."}, GateSchedule: "03:00"},
	}}
	jobs := nightlyJobs(cfg, map[string]bool{})
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs: %+v", len(jobs), jobs)
	}
	if j := jobs[0]; j.Project != "on" || j.QueueFile != "STATUS-P1.md" || len(j.Gates) != 1 || j.Gates[0] != "go test ./..." ||
		j.Schedule.Hour != 3 || j.Schedule.Minute != 0 {
		t.Errorf("job 0: %+v", j)
	}
	if j := jobs[1]; j.Project != "own-gates" || j.QueueFile != "NIGHT.md" || j.Gates[0] != "go vet ./..." ||
		j.Schedule.Hour != 4 || j.Schedule.Minute != 15 {
		t.Errorf("job 1: %+v", j)
	}
	if got := nightlyJobs(nil, map[string]bool{}); len(got) != 0 {
		t.Errorf("nil config scheduled %d jobs", len(got))
	}
}
