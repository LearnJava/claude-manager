package session

import (
	"fmt"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
)

// Permission escalation settings (PLAN.md §16.9): permission_timeout,
// permission_timeout_action, permission_native_notification and
// permission_sound. All are read from the live config at the moment a
// request is queued, so a change made in the UI applies to the very next
// request, also in sessions that are already running.

// permissionAlertFlags returns whether the frontend should show a Windows
// toast and play a sound for a newly queued permission request.
func (m *SessionManager) permissionAlertFlags() (notify, sound bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.Settings.PermissionNativeNotification, m.cfg.Settings.PermissionSound
}

// schedulePermissionTimeout arms the automatic answer for a queued request
// when permission_timeout > 0. The timer is not cancelled when the user
// answers first: permissionTimedOut re-checks the queue and does nothing for
// a request that is no longer pending.
func (m *SessionManager) schedulePermissionTimeout(sessionID, requestID string) {
	m.mu.Lock()
	timeout := m.cfg.Settings.PermissionTimeout
	action := m.cfg.Settings.PermissionTimeoutAction
	m.mu.Unlock()
	if timeout <= 0 {
		return
	}
	time.AfterFunc(time.Duration(timeout)*time.Second, func() {
		defer logger.Recover("manager.permission_timeout", "id", sessionID)
		m.permissionTimedOut(sessionID, requestID, timeout, action)
	})
}

// permissionTimedOut applies permission_timeout_action to a request that is
// still unanswered: "deny" answers it with deny (the agent sees a refusal
// and carries on), "pause_session" drops it and stops the session hard so
// crash recovery can resume it later.
func (m *SessionManager) permissionTimedOut(sessionID, requestID string, timeoutSec int, action string) {
	if _, ok := m.queue.Get(requestID); !ok {
		return // already answered
	}
	ms := m.get(sessionID)
	if ms == nil {
		m.queue.Remove(requestID)
		return
	}
	logger.L.Info("permission.timeout", "id", sessionID, "request", requestID, "action", action)
	var msg string
	if action == "pause_session" {
		msg = fmt.Sprintf("Permission request unanswered for %ds — session paused (permission_timeout_action = pause_session)", timeoutSec)
	} else {
		msg = fmt.Sprintf("Permission request unanswered for %ds — denied automatically (permission_timeout)", timeoutSec)
	}
	ms.session.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
		Time: time.Now(), Level: "system", Source: "manager", Message: msg,
	}})
	if action == "pause_session" {
		m.queue.Remove(requestID)
		if err := m.StopSession(sessionID, false); err != nil {
			logger.L.Warn("permission.timeout.stop_failed", "id", sessionID, "err", err)
		}
		return
	}
	if err := m.RespondPermission(sessionID, requestID, "deny"); err != nil {
		logger.L.Warn("permission.timeout.deny_failed", "id", sessionID, "err", err)
	}
}
