package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
	"claude-manager/internal/worker"
)

// rpcRequest is a JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// rpcResponse is a JSON-RPC 2.0 response envelope.
type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// handler processes decoded RPC params and returns (result, error).
type handler func(params json.RawMessage) (any, error)

// handleRPC dispatches a JSON-RPC 2.0 POST request.
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "parse error: "+err.Error())
		return
	}
	h, ok := s.registry[req.Method]
	if !ok {
		writeRPCError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
		return
	}
	params := req.Params
	if params == nil {
		params = json.RawMessage(`{}`)
	}
	result, err := h(params)
	if err != nil {
		writeRPCError(w, req.ID, -32000, err.Error())
		return
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeRPCError(w http.ResponseWriter, id any, code int, msg string) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// buildRegistry constructs the full RPC dispatch table. Every entry here is
// automatically available to both the control-plane and (via app.go wrappers)
// the Wails frontend.
func (s *Server) buildRegistry() map[string]handler {
	m := s.manager
	reg := make(map[string]handler)

	// ── Session lifecycle ─────────────────────────────────────────────────────

	reg["StartSession"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
			Session string `json:"session"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.StartSession(args.Project, args.Session)
	}

	reg["StartSessionWithOverride"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
			Session string `json:"session"`
			Model   string `json:"model"`
			Effort  string `json:"effort"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.StartSessionWithOverride(args.Project, args.Session, args.Model, args.Effort)
	}

	reg["StopSession"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID   string `json:"id"`
			Soft bool   `json:"soft"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.StopSession(args.ID, args.Soft)
	}

	reg["StopAll"] = func(_ json.RawMessage) (any, error) {
		m.StopAll()
		return nil, nil
	}

	reg["RestartSession"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.RestartSession(args.ID)
	}

	reg["ResumeSession"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.ResumeSession(args.ID)
	}

	reg["StartProject"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.StartProject(args.Project)
	}

	reg["StopProject"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.StopProject(args.Project)
	}

	// ── Bidirectional streaming + permissions ─────────────────────────────────

	reg["SendMessage"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.SendMessage(args.ID, args.Message)
	}

	reg["RespondPermission"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID        string `json:"id"`
			RequestID string `json:"request_id"`
			Decision  string `json:"decision"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.RespondPermission(args.ID, args.RequestID, args.Decision)
	}

	reg["GetPendingPermissions"] = func(_ json.RawMessage) (any, error) {
		return m.GetPendingPermissions(), nil
	}

	// ── State / history / metrics ─────────────────────────────────────────────

	reg["GetAllSessions"] = func(_ json.RawMessage) (any, error) {
		return m.GetAllSessions(), nil
	}

	reg["GetSession"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		st, ok := m.GetSession(args.ID)
		if !ok {
			return nil, nil
		}
		return st, nil
	}

	reg["GetSessionLog"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID     string `json:"id"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.GetSessionLog(args.ID, args.Offset, args.Limit)
	}

	reg["GetHistory"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
			Limit   int    `json:"limit"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.GetHistory(args.Project, args.Limit)
	}

	reg["GetSessionMetrics"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.GetSessionMetrics(args.ID)
	}

	reg["GetDailyCost"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Date string `json:"date"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.GetDailyCost(args.Date)
	}

	reg["GetProjectCost"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
			Days    int    `json:"days"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.GetProjectCost(args.Project, args.Days)
	}

	reg["GetRateLimitStatus"] = func(_ json.RawMessage) (any, error) {
		return m.GetRateLimitStatus(), nil
	}

	reg["ClearSessionState"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
			Session string `json:"session"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		m.ClearSessionState(args.Project, args.Session)
		return nil, nil
	}

	reg["GetSessionState"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
			Session string `json:"session"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.GetSessionState(args.Project, args.Session), nil
	}

	// ── Pre-flight plans (PLAN.md section 17) ────────────────────────────────

	reg["RunPreflight"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
			Task    string `json:"task"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.RunPreflight(args.Project, args.Task)
	}

	reg["ApprovePlan"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Plan analysis.TaskPlan `json:"plan"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.ApprovePlan(&args.Plan)
	}

	reg["ExecutePlan"] = func(p json.RawMessage) (any, error) {
		var args struct {
			PlanID json.RawMessage `json:"plan_id"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		id, err := parsePlanID(args.PlanID)
		if err != nil {
			return nil, err
		}
		return nil, m.ExecutePlan(id)
	}

	reg["GetPlan"] = func(p json.RawMessage) (any, error) {
		var args struct {
			PlanID json.RawMessage `json:"plan_id"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		id, err := parsePlanID(args.PlanID)
		if err != nil {
			return nil, err
		}
		return m.GetPlan(id)
	}

	// ── Mixed programming (MIXED-TASKS.md MP-05/MP-07) ───────────────────────

	reg["RegisterMixedBrief"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID           string `json:"id"`
			Task         string `json:"task"`
			SystemPrompt string `json:"system_prompt"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		if args.ID == "" || args.Task == "" {
			return nil, fmt.Errorf("register brief: id and task are required")
		}
		m.RegisterMixedBrief(args.ID, worker.Brief{Task: args.Task, SystemPrompt: args.SystemPrompt})
		return nil, nil
	}

	reg["DispatchMixedTask"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
			BriefID string `json:"brief_id"`
			Worker  string `json:"worker"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.DispatchMixedTask(args.Project, args.BriefID, args.Worker)
	}

	reg["GetMixedRounds"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.GetMixedRounds(args.Project)
	}

	reg["GetMixedQuality"] = func(p json.RawMessage) (any, error) {
		var args struct {
			Project string `json:"project"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return m.GetMixedQuality(args.Project)
	}

	reg["CancelMixedTask"] = func(p json.RawMessage) (any, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(p, &args); err != nil {
			return nil, err
		}
		return nil, m.CancelMixedTask(args.ID)
	}

	// ── Config (via AppAPI) ───────────────────────────────────────────────────

	if s.app != nil {
		a := s.app
		reg["GetConfig"] = func(_ json.RawMessage) (any, error) {
			return a.GetConfig(), nil
		}
		reg["UpdateConfig"] = func(p json.RawMessage) (any, error) {
			var args struct {
				Config config.AppConfig `json:"config"`
			}
			if err := json.Unmarshal(p, &args); err != nil {
				return nil, err
			}
			return nil, a.UpdateConfig(args.Config)
		}
	}

	return reg
}

// parsePlanID accepts a plan id as either a JSON number (Wails/JS callers) or
// a numeric string (the MCP tool schema passes string params).
func parsePlanID(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("plan_id is required")
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		n, convErr := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if convErr != nil {
			return 0, fmt.Errorf("plan_id %q is not a number", s)
		}
		return n, nil
	}
	return 0, fmt.Errorf("plan_id has unsupported type: %s", string(raw))
}
