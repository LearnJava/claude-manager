package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/control"
	"claude-manager/internal/session"
	"claude-manager/internal/store"
)

// Instance is one throw-away manager: a real SessionManager over fakeclaude,
// a real store and a real control-plane on a free loopback port, all in a temp
// folder. Every task gets its own, so tasks cannot see each other's state.
type Instance struct {
	Dir   string // root of everything the instance owns
	Addr  string // control-plane address, http://127.0.0.1:port
	Token string
	Mgr   *session.SessionManager
	Store *store.Store
	App   *evalApp

	cancel context.CancelFunc
}

// NewInstance starts an instance whose sessions run claudePath (fakeclaude)
// with scenarios from scenarioDir.
func NewInstance(claudePath, scenarioDir string) (*Instance, error) {
	dir, err := os.MkdirTemp("", "cm-mcp-eval")
	if err != nil {
		return nil, err
	}
	if err := os.Setenv("FAKECLAUDE_SCENARIO", scenarioDir); err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(dir, "config.toml")
	cfg, err := config.Load(cfgPath) // no file: the empty default config
	if err != nil {
		return nil, err
	}
	cfg.Settings.ClaudePath = claudePath
	cfg.Settings.DefaultRetryDelay = 1
	cfg.Settings.RateLimitPause = 1
	cfg.Settings.CrashRecovery = false
	cfg.Settings.SessionStartDelay = 0
	cfg.Optimization.ExperienceTracking = true // permission decisions are recorded

	st, err := store.New(filepath.Join(dir, "cm.db"))
	if err != nil {
		return nil, err
	}
	ce := control.NewControlEmitter(500)
	mgr := session.NewSessionManager(cfg, cfgPath, st, ce)
	app := &evalApp{cfg: cfg, path: cfgPath, mgr: mgr, st: st}
	token := control.GenerateToken()
	srv := control.NewServer(mgr, app, ce, token)
	ln, err := srv.Listen("0")
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, ln) }()
	return &Instance{
		Dir: dir, Addr: "http://" + ln.Addr().String(), Token: token,
		Mgr: mgr, Store: st, App: app, cancel: cancel,
	}, nil
}

// Close stops every session and removes the instance's folder.
func (in *Instance) Close() {
	in.Mgr.StopAll()
	in.cancel()
	_ = in.Store.Close()
	_ = os.RemoveAll(in.Dir)
}

// AddProject registers a project over a fresh folder under the instance dir
// and returns the folder.
func (in *Instance) AddProject(name string, sessions ...config.SessionConfig) (string, error) {
	path := filepath.Join(in.Dir, "proj-"+name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	cfg := in.App.snapshot()
	cfg.Projects = append(cfg.Projects, config.ProjectConfig{Name: name, Path: path, Sessions: sessions})
	return path, in.App.UpdateConfig(cfg)
}

// Session finds a session in the config.
func (in *Instance) Session(project, name string) (config.SessionConfig, bool) {
	for _, p := range in.App.snapshot().Projects {
		if p.Name != project {
			continue
		}
		for _, s := range p.Sessions {
			if s.Name == name {
				return s, true
			}
		}
	}
	return config.SessionConfig{}, false
}

// Project finds a project in the config.
func (in *Instance) Project(name string) (config.ProjectConfig, bool) {
	for _, p := range in.App.snapshot().Projects {
		if p.Name == name {
			return p, true
		}
	}
	return config.ProjectConfig{}, false
}

// WaitStatus polls until the session reaches status or the timeout passes.
func (in *Instance) WaitStatus(id, status string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st, ok := in.Mgr.GetSession(id); ok && string(st.Status) == status {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	st, _ := in.Mgr.GetSession(id)
	return fmt.Errorf("session %s: status %q after %s, wanted %q", id, st.Status, timeout, status)
}
