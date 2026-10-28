package logship

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var secrets = []string{
	"sk-ant-api03-AbCdEf_123-xyz",
	"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
	"gho_abcdefghijklmnopqrstuvwxyz0123456789",
	"github_pat_11ABCDEFG0abcdefghijklmnop_qrstuvwxyz",
	"AKIAABCDEFGHIJKLMNOP",
	"123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw0",
	"-----BEGIN RSA PRIVATE KEY-----\nMIIEabc\ndef==\n-----END RSA PRIVATE KEY-----",
	"my-control-token-42",
}

func TestMaskEachSecret(t *testing.T) {
	for _, s := range secrets {
		got := Mask("before "+s+" after", []string{"my-control-token-42"})
		if got != "before *** after" {
			t.Errorf("secret %q -> %q", s, got)
		}
	}
	if got := Mask("plain text, key=abc", nil); got != "plain text, key=abc" {
		t.Fatalf("plain text changed: %q", got)
	}
	if got := Mask("x", []string{""}); got != "x" {
		t.Fatalf("empty extra: %q", got)
	}
}

func TestShipMasksRunLogsManagerLogsAndMachine(t *testing.T) {
	e := newEnv(t)
	name := writeLog(t, e.proj, e.now.Add(-time.Hour))
	logPath := filepath.Join(e.proj, ".claude-manager", "logs", name)
	if err := os.WriteFile(logPath, []byte("token "+secrets[0]+" and extra-secret-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mdir, "app.log"), []byte("t "+secrets[1]+" "+secrets[7]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Ship(t.Context(), Options{
		Repo: e.bare, Host: "pc1", Build: "abc " + secrets[7], DataDir: e.data,
		Projects:       []Project{{Name: "proj", Path: e.proj}},
		Now:            func() time.Time { return e.now },
		ManagerLogsDir: mdir,
		ExtraSecrets:   []string{"extra-secret-1", secrets[7]},
	})
	if err != nil || !res.Pushed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	for _, p := range []string{"logs/pc1/proj/2026-10/" + name, "logs/pc1/manager/app-2026-10-27.log", "logs/pc1/machine.md"} {
		out := run(t, e.bare, "show", "main:"+p)
		for _, s := range append(secrets, "extra-secret-1") {
			if strings.Contains(out, s) {
				t.Errorf("%s leaks %q", p, s)
			}
		}
	}
	if out := run(t, e.bare, "show", "main:logs/pc1/manager/app-2026-10-27.log"); strings.TrimSpace(out) != "t *** ***" {
		t.Fatalf("manager log=%q", out)
	}
	md := run(t, e.bare, "show", "main:logs/pc1/machine.md")
	if !strings.Contains(md, "Go: go") || !strings.Contains(md, "| proj |") {
		t.Fatalf("machine.md=%q", md)
	}
}

func shipWithManager(t *testing.T, e *env, mdir string) (Result, error) {
	return Ship(t.Context(), Options{
		Repo: e.bare, Host: "pc1", Build: "b", DataDir: e.data,
		Projects: []Project{{Name: "proj", Path: e.proj}}, Now: func() time.Time { return e.now },
		ManagerLogsDir: mdir,
	})
}

func TestManagerLogOffsetAppendAndTruncate(t *testing.T) {
	e := newEnv(t)
	mdir := t.TempDir()
	app := filepath.Join(mdir, "app.log")
	remote := func() string { return run(t, e.bare, "show", "main:logs/pc1/manager/app-2026-10-27.log") }

	os.WriteFile(app, []byte("one\n"), 0o644)
	if res, err := shipWithManager(t, e, mdir); err != nil || !res.Pushed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	f, _ := os.OpenFile(app, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("two\n")
	f.Close()
	if _, err := shipWithManager(t, e, mdir); err != nil {
		t.Fatal(err)
	}
	if got := remote(); got != "one\ntwo" {
		t.Fatalf("after append=%q", got)
	}
	// Idle pass: no new commit.
	before := e.commits(t)
	if res, err := shipWithManager(t, e, mdir); err != nil || res.Pushed || e.commits(t) != before {
		t.Fatalf("idle res=%+v err=%v", res, err)
	}
	// Shorter file (rotation): shipped from offset 0.
	os.WriteFile(app, []byte("r\n"), 0o644)
	if _, err := shipWithManager(t, e, mdir); err != nil {
		t.Fatal(err)
	}
	if got := remote(); got != "one\ntwo\nr" {
		t.Fatalf("after truncate=%q", got)
	}
}

func TestManagerLogNotDoubledAfterRejectedPush(t *testing.T) {
	e := newEnv(t)
	mdir := t.TempDir()
	os.WriteFile(filepath.Join(mdir, "app.log"), []byte("line\n"), 0o644)
	hook := filepath.Join(e.bare, "hooks", "pre-receive")
	os.MkdirAll(filepath.Dir(hook), 0o755)
	os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if res, err := shipWithManager(t, e, mdir); err == nil || res.Pushed {
		t.Fatalf("expected failure, res=%+v", res)
	}
	os.Remove(hook)
	if res, err := shipWithManager(t, e, mdir); err != nil || !res.Pushed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := run(t, e.bare, "show", "main:logs/pc1/manager/app-2026-10-27.log"); got != "line" {
		t.Fatalf("log=%q", got)
	}
}
