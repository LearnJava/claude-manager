package permission

import (
	"context"
	"errors"

	"claude-manager/internal/config"
)

// Source identifies who resolved a permission request.
type Source string

const (
	SourceConfigRule  Source = "config_rule"
	SourceRuntimeRule Source = "runtime_rule"
	SourceUser        Source = "user"
	SourceCanceled    Source = "canceled"
)

// Result is the outcome of PermissionHandler.Handle.
type Result struct {
	Response   PermissionResponse      // the decision (with original user choice preserved)
	CLIAnswer  string                  // "allow" or "deny", ready for stdin
	Source     Source                  // who decided
	MatchedBy  *config.PermissionRule  // non-nil when matched by a rule
	AddedRule  *config.PermissionRule  // non-nil when a runtime rule was added
	Persist    bool                    // true when the caller should persist a TOML rule
}

// ErrNoResponse is returned by Handle when the response channel is closed
// before a decision arrives.
var ErrNoResponse = errors.New("permission: response channel closed without decision")

// PermissionHandler resolves permission requests by checking config rules,
// runtime rules, and (as a last resort) blocking on a user-supplied response
// channel via the pending queue.
type PermissionHandler struct {
	configRules  []config.PermissionRule
	runtimeRules *RuntimeRuleSet
	queue        *PendingQueue
}

// NewPermissionHandler wires together the static (config) rules and the
// mutable runtime rules / pending queue. queue and runtimeRules must not be
// nil — pass NewPendingQueue() / NewRuntimeRuleSet() if you do not have one.
func NewPermissionHandler(configRules []config.PermissionRule, runtimeRules *RuntimeRuleSet, queue *PendingQueue) *PermissionHandler {
	if runtimeRules == nil {
		runtimeRules = NewRuntimeRuleSet()
	}
	if queue == nil {
		queue = NewPendingQueue()
	}
	return &PermissionHandler{
		configRules:  configRules,
		runtimeRules: runtimeRules,
		queue:        queue,
	}
}

// ConfigRules returns the config-supplied rules (in declaration order).
func (h *PermissionHandler) ConfigRules() []config.PermissionRule {
	out := make([]config.PermissionRule, len(h.configRules))
	copy(out, h.configRules)
	return out
}

// RuntimeRules exposes the runtime rule set so callers can inspect or extend it.
func (h *PermissionHandler) RuntimeRules() *RuntimeRuleSet {
	return h.runtimeRules
}

// Queue exposes the pending queue.
func (h *PermissionHandler) Queue() *PendingQueue {
	return h.queue
}

// SetConfigRules replaces the config rule list. Useful when the user edits
// permission rules at runtime via the settings UI.
func (h *PermissionHandler) SetConfigRules(rules []config.PermissionRule) {
	h.configRules = rules
}

// CheckAuto checks config rules then runtime rules. It returns ("allow"/"deny",
// source, matched-rule, true) when a rule decides automatically. A matched
// rule with decision "ask" short-circuits the search and returns ok=false so
// the caller knows the UI must be involved (the "ask" rule overrides any
// later "allow"/"deny" rules — see PLAN.md section 16.5).
func (h *PermissionHandler) CheckAuto(req PermissionRequest) (decision string, source Source, matched *config.PermissionRule, ok bool) {
	if dec, rule, found := MatchRules(h.configRules, req); found {
		if dec == "ask" {
			r := rule
			return "", SourceConfigRule, &r, false
		}
		r := rule
		return dec, SourceConfigRule, &r, true
	}
	if dec, rule, found := MatchRules(h.runtimeRules.All(), req); found {
		if dec == "ask" {
			r := rule
			return "", SourceRuntimeRule, &r, false
		}
		r := rule
		return dec, SourceRuntimeRule, &r, true
	}
	return "", "", nil, false
}

// Handle resolves req. If a config or runtime rule auto-decides, the rule's
// decision is returned immediately. Otherwise req is added to the pending
// queue, notify is invoked (so the UI layer can render a banner / emit an
// event), and Handle blocks until either responseCh delivers a decision or
// ctx is canceled. On return the request has been removed from the queue and
// any "allow_session"/"allow_similar" decision has been recorded in the
// runtime rule set. "allow_always"/"deny_always" set Result.Persist=true so
// the caller can persist the rule to TOML.
//
// notify and responseCh must be non-nil when no rule auto-resolves.
func (h *PermissionHandler) Handle(
	ctx context.Context,
	req PermissionRequest,
	responseCh <-chan PermissionResponse,
	notify func(PermissionRequest),
) (Result, error) {
	if decision, source, matched, ok := h.CheckAuto(req); ok {
		return Result{
			Response:  PermissionResponse{RequestID: req.ID, Decision: decision},
			CLIAnswer: CLIDecision(decision),
			Source:    source,
			MatchedBy: matched,
		}, nil
	}

	h.queue.Add(req)
	defer h.queue.Remove(req.ID)

	if notify != nil {
		notify(req)
	}

	if responseCh == nil {
		return Result{}, errors.New("permission: nil response channel")
	}

	select {
	case <-ctx.Done():
		return Result{
			Response:  PermissionResponse{RequestID: req.ID, Decision: "deny"},
			CLIAnswer: "deny",
			Source:    SourceCanceled,
		}, ctx.Err()
	case resp, ok := <-responseCh:
		if !ok {
			return Result{}, ErrNoResponse
		}
		resp.RequestID = req.ID
		result := Result{
			Response:  resp,
			CLIAnswer: CLIDecision(resp.Decision),
			Source:    SourceUser,
		}
		h.applyUserDecision(req, resp, &result)
		return result, nil
	}
}

// applyUserDecision records runtime rules and Persist flags based on the
// user's choice (allow_session / allow_similar / allow_always / deny_always).
func (h *PermissionHandler) applyUserDecision(req PermissionRequest, resp PermissionResponse, result *Result) {
	switch resp.Decision {
	case "allow_session", "allow_similar":
		rule := config.PermissionRule{Tool: req.Tool, Pattern: req.Pattern(), Decision: "allow"}
		h.runtimeRules.Add(rule.Tool, rule.Pattern, rule.Decision)
		result.AddedRule = &rule
	case "allow_always":
		rule := config.PermissionRule{Tool: req.Tool, Pattern: req.Pattern(), Decision: "allow"}
		h.runtimeRules.Add(rule.Tool, rule.Pattern, rule.Decision)
		result.AddedRule = &rule
		result.Persist = true
	case "deny_always":
		rule := config.PermissionRule{Tool: req.Tool, Pattern: req.Pattern(), Decision: "deny"}
		h.runtimeRules.Add(rule.Tool, rule.Pattern, rule.Decision)
		result.AddedRule = &rule
		result.Persist = true
	}
}
