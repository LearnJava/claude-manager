package session

import (
	"strings"
	"time"
)

// TurnFailure is the classified outcome of a failed/aborted turn (RT-04),
// replacing six independent atomics (rateLimited/authErrorHit/
// sessionNotFoundHit/contextRestartHit/stepLimitHit, plus the
// continue-session marker which stays its own thing — see below) that used
// to be set from six different call sites across parser.go, hermes_parser.go,
// session.go and hermes_runtime.go. An adapter (parser.go's handleResult,
// hermes_parser.go's Parse, or the stderr classifiers below) produces one of
// these; Session.applyFailure (ratelimit.go) is the single place it becomes
// session state. See docs/runtimes.md "Исход хода" for the full table.
type TurnFailure struct {
	Kind    string
	Message string
	// ResetsAt is set only for KindRateLimit when a reset time is known
	// (structured rate_limit_event, or a parsed "resets HH:MM" in text).
	ResetsAt time.Time
}

// TurnFailure.Kind values. KindContinueSession (the agent's own "no, stop
// here" answer to the continue-session question) is intentionally NOT one of
// these: it is not a failure, it is a normal answered question, and stays on
// Session.continueMarkerHit exactly as before RT-04.
const (
	KindRateLimit       = "rate_limit"
	KindAuth            = "auth"
	KindSessionNotFound = "session_not_found"
	KindContextRestart  = "context_restart"
	KindStepLimit       = "step_limit"
	KindTransientAPI    = "transient_api"
	KindOther           = "other"
)

// isAuthError reports whether a line (stderr or a result's own text) is a
// 403/authentication failure. Shared by both runtimes' stderr classifiers and
// Claude's result-text check (auth can surface either way — see
// docs/runtimes.md §5).
func isAuthError(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(line, "403") &&
		(strings.Contains(lower, "forbidden") ||
			strings.Contains(lower, "authenticate") ||
			strings.Contains(lower, "unauthorized"))
}

// transientAPIMarkers are the substrings that make an "API Error: ..." result
// a transient failure: the network or the API itself was briefly unreachable,
// and the same request will succeed once it is back. Observed live on a Lumen
// session: "API Error: Connection refused — a firewall or proxy may be
// blocking it (ECONNREFUSED)" ended a 30-minute run cleanly, and the Run loop
// counted it as an unfinished task.
var transientAPIMarkers = []string{
	"econnrefused", "econnreset", "etimedout", "enotfound", "eai_again",
	"enetunreach", "ehostunreach", "epipe", "socket hang up",
	"connection refused", "connection error", "connection reset",
	"request timed out", "fetch failed", "network error",
	"overloaded", "api_error", "internal server error", "bad gateway",
	"service unavailable", "gateway timeout",
	"api error: 500", "api error: 502", "api error: 503", "api error: 504", "api error: 529",
}

// isTransientAPIError reports whether a Claude result's own text is the CLI's
// "API Error: ..." for a network failure or a 5xx/overloaded API response.
// Requires the "API Error" prefix so a normal final answer that merely
// mentions ECONNREFUSED (e.g. while debugging a server) never matches.
func isTransientAPIError(text string) bool {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "API Error") {
		return false
	}
	lower := strings.ToLower(t)
	for _, m := range transientAPIMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// isSessionNotFoundError reports whether a Hermes stderr line is its
// "Session not found: <id>" message — the --resume target does not exist in
// the Hermes profile's own session store. Distinct from isAuthError: Hermes
// exits non-zero the same way for both, but this one is not a credentials
// problem and resuming the same id again only repeats it forever (see
// errSessionNotFound).
func isSessionNotFoundError(line string) bool {
	return strings.Contains(line, "Session not found:")
}

// ClassifyStderrLine classifies one Claude stderr line into a TurnFailure,
// or nil if it carries no recognized failure signal. Used by drainStderr;
// rate-limit text detection stays on detectRateLimitText/onRateLimit
// (ratelimit.go) because a throttle is not always a failure — an
// "allowed"/"allowed_warning" status is informational, so the boolean
// throttled() decision has to run before a TurnFailure is worth building.
func ClassifyStderrLine(line string) *TurnFailure {
	if isAuthError(line) {
		return &TurnFailure{Kind: KindAuth, Message: line}
	}
	return nil
}

// ClassifyHermesStderrLine is ClassifyStderrLine for Hermes stderr, which
// additionally recognizes the session-not-found case Claude has no
// equivalent for (see isSessionNotFoundError).
func ClassifyHermesStderrLine(line string) *TurnFailure {
	if isSessionNotFoundError(line) {
		return &TurnFailure{Kind: KindSessionNotFound, Message: line}
	}
	if isAuthError(line) {
		return &TurnFailure{Kind: KindAuth, Message: line}
	}
	return nil
}
