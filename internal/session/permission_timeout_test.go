package session

import (
	"testing"
	"time"

	"claude-manager/internal/permission"
)

func TestPermissionTimedOut_DeniesPendingRequest(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "P1")
	m.queue.Add(permission.PermissionRequest{ID: "r1", SessionID: ms.session.ID, Tool: "Bash"})

	m.permissionTimedOut(ms.session.ID, "r1", 5, "deny")

	if _, ok := m.queue.Get("r1"); ok {
		t.Error("request still queued after the timeout denied it")
	}
}

func TestPermissionTimedOut_AnsweredRequestIsLeftAlone(t *testing.T) {
	m := newTestManager(t)
	ms := addStubSession(m, "lumen", "P1")
	m.queue.Add(permission.PermissionRequest{ID: "other", SessionID: ms.session.ID, Tool: "Bash"})

	// "r1" was answered by the user before the timer fired: nothing to do,
	// and an unrelated pending request must stay queued.
	m.permissionTimedOut(ms.session.ID, "r1", 5, "pause_session")

	if _, ok := m.queue.Get("other"); !ok {
		t.Error("unrelated pending request was dropped")
	}
}

func TestSchedulePermissionTimeout_FiresAfterTimeout(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Settings.PermissionTimeout = 1
	m.cfg.Settings.PermissionTimeoutAction = "deny"
	ms := addStubSession(m, "lumen", "P1")
	m.queue.Add(permission.PermissionRequest{ID: "r1", SessionID: ms.session.ID, Tool: "Bash"})

	m.schedulePermissionTimeout(ms.session.ID, "r1")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := m.queue.Get("r1"); !ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("request still queued 3s after a 1s permission_timeout")
}

func TestSchedulePermissionTimeout_ZeroWaitsForever(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Settings.PermissionTimeout = 0
	ms := addStubSession(m, "lumen", "P1")
	m.queue.Add(permission.PermissionRequest{ID: "r1", SessionID: ms.session.ID, Tool: "Bash"})

	m.schedulePermissionTimeout(ms.session.ID, "r1")
	time.Sleep(200 * time.Millisecond)

	if _, ok := m.queue.Get("r1"); !ok {
		t.Error("request answered although permission_timeout = 0")
	}
}

func TestPermissionAlertFlags_ReadLiveConfig(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Settings.PermissionNativeNotification = true
	m.cfg.Settings.PermissionSound = false
	if n, s := m.permissionAlertFlags(); !n || s {
		t.Errorf("got notify=%v sound=%v, want true/false", n, s)
	}

	next := *m.cfg
	next.Settings.PermissionNativeNotification = false
	next.Settings.PermissionSound = true
	m.SetConfig(&next)
	if n, s := m.permissionAlertFlags(); n || !s {
		t.Errorf("after SetConfig got notify=%v sound=%v, want false/true", n, s)
	}
}
