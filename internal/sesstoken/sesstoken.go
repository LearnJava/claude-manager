// Package sesstoken issues per-session control-plane tokens (MCP-19).
//
// The manager gives every CLI session its own token in CM_CONTROL_TOKEN. The
// control-plane resolves a request carrying such a token to the session id, so
// the caller identity no longer depends on a header the client writes itself.
// It lives in its own package because both internal/session (issues) and
// internal/control (resolves) need it and control must not depend on the
// launch code.
package sesstoken

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

var (
	mu    sync.RWMutex
	byTok = map[string]string{} // token -> "{project}/{session}"
)

// Issue creates a fresh token for the session id and remembers it.
func Issue(id string) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("sesstoken: " + err.Error())
	}
	tok := "cms-" + hex.EncodeToString(b)
	mu.Lock()
	byTok[tok] = id
	mu.Unlock()
	return tok
}

// Revoke forgets the token; call it when the session process has ended.
func Revoke(token string) {
	mu.Lock()
	delete(byTok, token)
	mu.Unlock()
}

// Lookup returns the session id a token was issued for.
func Lookup(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	mu.RLock()
	id, ok := byTok[token]
	mu.RUnlock()
	return id, ok
}
