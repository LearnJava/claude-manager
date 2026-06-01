package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
	"claude-manager/internal/permission"
	"claude-manager/internal/session"
	"claude-manager/internal/store"
)

// ManagerAPI is the subset of *session.SessionManager consumed by the RPC
// layer. Using an interface makes Server fully testable with a mock.
type ManagerAPI interface {
	StartSession(project, name string) error
	StartSessionWithOverride(project, name, model, effort string) error
	StopSession(id string, soft bool) error
	StopAll()
	RestartSession(id string) error
	ResumeSession(id string) error
	StartProject(project string) error
	StopProject(project string) error
	SendMessage(id, message string) error
	RespondPermission(id, requestID, decision string) error
	GetPendingPermissions() []permission.PermissionRequest
	GetAllSessions() []session.SessionState
	GetSession(id string) (session.SessionState, bool)
	GetSessionLog(id string, offset, limit int) ([]*store.LogEntry, error)
	GetHistory(project string, limit int) ([]*store.SessionRun, error)
	GetSessionMetrics(id string) (session.SessionMetrics, error)
	GetDailyCost(date string) (float64, error)
	GetProjectCost(project string, days int) (float64, error)
	GetRateLimitStatus() *session.RateLimitInfo
	ClearSessionState(project, name string)
	GetSessionState(project, name string) *session.PersistedState
}

// AppAPI is the subset of *App used for config read/write.
type AppAPI interface {
	GetConfig() *config.AppConfig
	UpdateConfig(cfg config.AppConfig) error
}

// Server is the control-plane HTTP+WS server.
type Server struct {
	manager  ManagerAPI
	app      AppAPI
	emitter  *ControlEmitter
	token    string
	registry map[string]handler
	srv      *http.Server
}

// NewServer creates a Server and wires the HTTP mux.
func NewServer(manager ManagerAPI, app AppAPI, emitter *ControlEmitter, token string) *Server {
	s := &Server{
		manager: manager,
		app:     app,
		emitter: emitter,
		token:   token,
	}
	s.registry = s.buildRegistry()

	mux := http.NewServeMux()
	mux.Handle("/rpc", s.tokenAuth(http.HandlerFunc(s.handleRPC)))
	mux.Handle("/events", s.tokenAuth(http.HandlerFunc(s.handleEvents)))
	mux.Handle("/wait", s.tokenAuth(http.HandlerFunc(s.handleWait)))
	s.srv = &http.Server{
		Handler:     mux,
		ReadTimeout: 30 * time.Second,
		// WriteTimeout intentionally 0: streaming endpoints write indefinitely.
	}
	return s
}

// Start binds to 127.0.0.1:<port> (loopback only) and serves until ctx is
// cancelled.
func (s *Server) Start(ctx context.Context, port string) error {
	if port == "" {
		port = "7333"
	}
	addr := "127.0.0.1:" + port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("control: listen %s: %w", addr, err)
	}
	logger.L.Info("control.server.listening", "addr", addr)
	go func() {
		<-ctx.Done()
		_ = s.srv.Shutdown(context.Background())
	}()
	if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// tokenAuth enforces the X-CM-Token header on every request.
// For WebSocket upgrades (browsers cannot set custom headers), the token may
// also be supplied as the "token" query parameter.
// CORS headers are added for localhost/127.0.0.1 origins so that Playwright
// tests running against the Vite dev server can call the control-plane directly.
func (s *Server) tokenAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow cross-origin requests from local dev / test origins.
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CM-Token")
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Accept token via header (standard) or query parameter (WebSocket fallback).
		token := r.Header.Get("X-CM-Token")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token != s.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GenerateToken returns a cryptographically random 32-byte hex string.
func GenerateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("control: GenerateToken: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// StartFromEnv reads CM_CONTROL / CM_CONTROL_PORT / CM_CONTROL_TOKEN,
// starts the server in a goroutine when CM_CONTROL=1, and returns it (or nil
// when disabled). A generated token is printed to stdout so the caller can
// supply it to clients.
func StartFromEnv(ctx context.Context, manager ManagerAPI, app AppAPI, emitter *ControlEmitter) (*Server, error) {
	if os.Getenv("CM_CONTROL") != "1" {
		return nil, nil
	}
	token := os.Getenv("CM_CONTROL_TOKEN")
	if token == "" {
		token = GenerateToken()
		fmt.Printf("CM_CONTROL_TOKEN=%s\n", token)
	}
	port := os.Getenv("CM_CONTROL_PORT")
	if port == "" {
		port = "7333"
	}
	srv := NewServer(manager, app, emitter, token)
	go func() {
		if err := srv.Start(ctx, port); err != nil {
			logger.L.Error("control.server.error", "error", err)
		}
	}()
	return srv, nil
}
