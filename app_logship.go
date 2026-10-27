package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/buildinfo"
	"claude-manager/internal/control"
	"claude-manager/internal/logger"
	"claude-manager/internal/logship"
)

// ---- Log shipping (internal/logship) ----
//
// With logs_repo set, run logs of every project and the manager's own logs are
// pushed into logs/<host>/ of one private repository: shortly after start, on
// a timer, after a settings save and from the Settings button.

// LogShipStatus is what Settings and get_log_ship_status show.
type LogShipStatus struct {
	Enabled bool   `json:"enabled"`
	Repo    string `json:"repo"`
	Host    string `json:"host"`
	LastRun string `json:"lastRun"` // RFC3339, "" before the first pass this run
	Files   int    `json:"files"`
	Error   string `json:"error"`
}

type logShipState struct {
	run  sync.Mutex // one pass at a time
	mu   sync.Mutex // guards last, ran
	last logship.Result
	ran  bool
	kick chan struct{}
}

var logShipSt = logShipState{kick: make(chan struct{}, 1)}

// startLogShipLoop ships once 30 s after start, then on every kick (settings
// saved) and every logs_interval_min minutes.
func (a *App) startLogShipLoop(ctx context.Context) {
	go func() {
		defer logger.Recover("logship.loop")
		first := time.NewTimer(30 * time.Second)
		defer first.Stop()
		for {
			var tick <-chan time.Time
			var timer *time.Timer
			if a.cfg != nil && a.cfg.Settings.LogsIntervalMin > 0 {
				timer = time.NewTimer(time.Duration(a.cfg.Settings.LogsIntervalMin) * time.Minute)
				tick = timer.C
			}
			select {
			case <-ctx.Done():
				return
			case <-first.C:
			case <-logShipSt.kick:
			case <-tick:
			}
			if timer != nil {
				timer.Stop()
			}
			if a.cfg == nil || strings.TrimSpace(a.cfg.Settings.LogsRepo) == "" {
				continue
			}
			if _, err := a.shipLogs(ctx); err != nil {
				logger.L.Warn("logship", "error", err)
			}
		}
	}()
}

// kickLogShip asks the loop for a pass without blocking.
func kickLogShip() {
	select {
	case logShipSt.kick <- struct{}{}:
	default:
	}
}

// shipLogs runs one pass and records its result. Passes never overlap.
func (a *App) shipLogs(ctx context.Context) (logship.Result, error) {
	if a.cfg == nil {
		return logship.Result{}, fmt.Errorf("config not loaded")
	}
	repo := strings.TrimSpace(a.cfg.Settings.LogsRepo)
	if repo == "" {
		return logship.Result{}, fmt.Errorf("logs_repo is empty")
	}
	logShipSt.run.Lock()
	defer logShipSt.run.Unlock()

	opt := logship.Options{
		Repo:           repo,
		Host:           buildinfo.Host(),
		Build:          buildinfo.Revision(),
		DataDir:        filepath.Dir(a.cfgPath),
		ManagerLogsDir: filepath.Join(filepath.Dir(a.cfgPath), "logs"),
	}
	for _, p := range a.cfg.Projects {
		opt.Projects = append(opt.Projects, logship.Project{Name: p.Name, Path: p.Path})
	}
	if ep, err := control.LoadEndpoint(); err == nil && ep != nil && ep.Token != "" {
		opt.ExtraSecrets = append(opt.ExtraSecrets, ep.Token)
	}
	res, err := logship.Ship(ctx, opt)
	if res.At.IsZero() {
		res.At = time.Now()
	}
	logShipSt.mu.Lock()
	logShipSt.last, logShipSt.ran = res, true
	logShipSt.mu.Unlock()
	if err != nil {
		return res, err
	}
	if res.Files > 0 {
		logger.L.Info("logship", "files", res.Files, "bytes", res.Bytes, "pushed", res.Pushed)
	}
	return res, nil
}

// ShipLogsNow runs one log-shipping pass right away.
func (a *App) ShipLogsNow() (logship.Result, error) {
	return a.shipLogs(a.bgCtx())
}

// GetLogShipStatus reports the log-shipping state for Settings.
func (a *App) GetLogShipStatus() LogShipStatus {
	st := LogShipStatus{Host: buildinfo.Host()}
	if a.cfg != nil {
		st.Repo = strings.TrimSpace(a.cfg.Settings.LogsRepo)
		st.Enabled = st.Repo != ""
	}
	logShipSt.mu.Lock()
	last, ran := logShipSt.last, logShipSt.ran
	logShipSt.mu.Unlock()
	if ran {
		st.LastRun = last.At.Format(time.RFC3339)
		st.Files = last.Files
		st.Error = last.Error
	}
	return st
}

func (c controlApp) ShipLogsNowResult() (any, error) {
	return c.App.ShipLogsNow()
}

func (c controlApp) LogShipStatusOf() any {
	return c.App.GetLogShipStatus()
}
