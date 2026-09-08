package control

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// homeInTemp points os.UserHomeDir at a temp dir so endpoint tests never touch
// the real ~/.claude-manager.
func homeInTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
	t.Setenv("HOME", dir)
	return dir
}

func TestEnabled_DefaultsToOn(t *testing.T) {
	cases := map[string]bool{
		"":      true, // unset — the control-plane is on by default
		"1":     true,
		"true":  true,
		"yes":   true,
		"0":     false,
		"false": false,
		"off":   false,
		"no":    false,
		" OFF ": false,
	}
	for value, want := range cases {
		t.Setenv("CM_CONTROL", value)
		if got := Enabled(); got != want {
			t.Errorf("CM_CONTROL=%q → Enabled()=%v, want %v", value, got, want)
		}
	}
	os.Unsetenv("CM_CONTROL")
	if !Enabled() {
		t.Error("unset CM_CONTROL must enable the control-plane")
	}
}

func TestEndpointRoundTrip(t *testing.T) {
	home := homeInTemp(t)

	if ep, err := LoadEndpoint(); err != nil || ep != nil {
		t.Fatalf("no endpoint file: got (%v, %v), want (nil, nil)", ep, err)
	}

	want := Endpoint{Addr: "http://127.0.0.1:7333", Token: "deadbeef", PID: 4242}
	if err := WriteEndpoint(want); err != nil {
		t.Fatalf("WriteEndpoint: %v", err)
	}
	path := filepath.Join(home, ".claude-manager", "control.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("endpoint not written to %s: %v", path, err)
	}

	got, err := LoadEndpoint()
	if err != nil || got == nil {
		t.Fatalf("LoadEndpoint: (%v, %v)", got, err)
	}
	if *got != want {
		t.Errorf("round trip: %+v, want %+v", *got, want)
	}

	RemoveEndpoint()
	if ep, err := LoadEndpoint(); err != nil || ep != nil {
		t.Errorf("after RemoveEndpoint: got (%v, %v), want (nil, nil)", ep, err)
	}
}

func TestLoadEndpoint_CorruptFileIsAnError(t *testing.T) {
	home := homeInTemp(t)
	dir := filepath.Join(home, ".claude-manager")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "control.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Distinct from "no file": a client should report a broken endpoint rather
	// than silently dial the default address with an empty token.
	if _, err := LoadEndpoint(); err == nil {
		t.Error("corrupt endpoint file: want an error")
	}
}

func TestStartFromEnv_PublishesAndRetractsTheEndpoint(t *testing.T) {
	homeInTemp(t)
	t.Setenv("CM_CONTROL", "1")
	t.Setenv("CM_CONTROL_TOKEN", "smoke-token")
	t.Setenv("CM_CONTROL_PORT", "0") // any free port

	ctx, cancel := context.WithCancel(context.Background())
	srv, err := StartFromEnv(ctx, &mockManager{}, &mockApp{}, NewControlEmitter(10))
	if err != nil {
		t.Fatalf("StartFromEnv: %v", err)
	}
	if srv == nil {
		t.Fatal("StartFromEnv returned no server with the control-plane enabled")
	}

	ep, err := LoadEndpoint()
	if err != nil || ep == nil {
		t.Fatalf("endpoint after start: (%v, %v)", ep, err)
	}
	if ep.Token != "smoke-token" || ep.PID != os.Getpid() {
		t.Errorf("endpoint %+v", ep)
	}
	// The advertised address must be the one actually bound, not the requested
	// port — with port 0 they differ, which is exactly the case that would
	// send a client somewhere else.
	if !strings.HasPrefix(ep.Addr, "http://127.0.0.1:") || strings.HasSuffix(ep.Addr, ":0") {
		t.Errorf("addr %q is not the bound address", ep.Addr)
	}

	// The endpoint must answer at the advertised address before we call it live.
	req, _ := http.NewRequest(http.MethodPost, ep.Addr+"/rpc", strings.NewReader(`{"method":"GetAllSessions"}`))
	req.Header.Set("X-CM-Token", ep.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("dial advertised endpoint: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("advertised endpoint answered %s", resp.Status)
	}
	// Shutdown waits for connections to go idle; a client-side keep-alive
	// connection left open would hold it past the deadline below.
	http.DefaultClient.CloseIdleConnections()

	cancel()
	// Serve returns after Shutdown, and the goroutine deletes the file then.
	deadline := time.Now().Add(5 * time.Second)
	for {
		ep, err := LoadEndpoint()
		if err == nil && ep == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("endpoint file outlived the server: path=%s ep=%+v err=%v", EndpointPath(), ep, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartFromEnv_DisabledStartsNothing(t *testing.T) {
	homeInTemp(t)
	t.Setenv("CM_CONTROL", "0")
	srv, err := StartFromEnv(context.Background(), &mockManager{}, &mockApp{}, NewControlEmitter(10))
	if err != nil || srv != nil {
		t.Fatalf("StartFromEnv with CM_CONTROL=0: (%v, %v)", srv, err)
	}
	if ep, _ := LoadEndpoint(); ep != nil {
		t.Error("disabled control-plane must not advertise an endpoint")
	}
}
