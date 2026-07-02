package permission

import (
	"fmt"
	"sync"
	"testing"
)

func TestPendingQueueAddAndGetAll(t *testing.T) {
	q := NewPendingQueue()
	if q.Len() != 0 {
		t.Fatalf("new queue should be empty")
	}
	q.Add(PermissionRequest{ID: "a", Tool: "Bash"})
	q.Add(PermissionRequest{ID: "b", Tool: "Edit"})
	all := q.GetAll()
	if len(all) != 2 {
		t.Fatalf("len = %d, want 2", len(all))
	}
	if all[0].ID != "a" || all[1].ID != "b" {
		t.Fatalf("insertion order not preserved: %+v", all)
	}
}

func TestPendingQueueAddReplaceSameID(t *testing.T) {
	q := NewPendingQueue()
	q.Add(PermissionRequest{ID: "a", Tool: "Bash", Command: "old"})
	q.Add(PermissionRequest{ID: "b", Tool: "Edit"})
	q.Add(PermissionRequest{ID: "a", Tool: "Bash", Command: "new"})
	all := q.GetAll()
	if len(all) != 2 {
		t.Fatalf("len = %d, want 2 (replace, not append)", len(all))
	}
	if all[0].ID != "a" || all[0].Command != "new" {
		t.Fatalf("first entry should be replaced: %+v", all[0])
	}
	if all[1].ID != "b" {
		t.Fatalf("second entry should remain b, got %q", all[1].ID)
	}
}

func TestPendingQueueRemove(t *testing.T) {
	q := NewPendingQueue()
	q.Add(PermissionRequest{ID: "a"})
	q.Add(PermissionRequest{ID: "b"})
	q.Add(PermissionRequest{ID: "c"})

	if !q.Remove("b") {
		t.Fatalf("Remove(b) should return true")
	}
	if q.Len() != 2 {
		t.Fatalf("Len = %d, want 2", q.Len())
	}
	all := q.GetAll()
	if all[0].ID != "a" || all[1].ID != "c" {
		t.Fatalf("unexpected order after remove: %+v", all)
	}
}

func TestPendingQueueRemoveMissing(t *testing.T) {
	q := NewPendingQueue()
	q.Add(PermissionRequest{ID: "a"})
	if q.Remove("missing") {
		t.Fatalf("Remove(missing) should return false")
	}
	if q.Len() != 1 {
		t.Fatalf("Len changed after no-op remove")
	}
}

func TestPendingQueueGet(t *testing.T) {
	q := NewPendingQueue()
	q.Add(PermissionRequest{ID: "x", Tool: "Bash", Command: "ls"})
	got, ok := q.Get("x")
	if !ok {
		t.Fatalf("Get(x) should succeed")
	}
	if got.Command != "ls" {
		t.Fatalf("Get returned wrong request: %+v", got)
	}
	if _, ok := q.Get("missing"); ok {
		t.Fatalf("Get(missing) should fail")
	}
}

func TestPendingQueueGetAllReturnsCopy(t *testing.T) {
	q := NewPendingQueue()
	q.Add(PermissionRequest{ID: "x", Command: "ls"})
	all := q.GetAll()
	all[0].Command = "mutated"
	got, _ := q.Get("x")
	if got.Command == "mutated" {
		t.Fatalf("GetAll must return a copy independent of internal storage")
	}
}

func TestPendingQueueBySession(t *testing.T) {
	q := NewPendingQueue()
	q.Add(PermissionRequest{ID: "1", SessionID: "lumen/P1"})
	q.Add(PermissionRequest{ID: "2", SessionID: "lumen/P2"})
	q.Add(PermissionRequest{ID: "3", SessionID: "lumen/P1"})
	got := q.BySession("lumen/P1")
	if len(got) != 2 {
		t.Fatalf("BySession(lumen/P1) len = %d, want 2", len(got))
	}
	if got[0].ID != "1" || got[1].ID != "3" {
		t.Fatalf("BySession returned wrong entries: %+v", got)
	}
	if other := q.BySession("none"); len(other) != 0 {
		t.Fatalf("BySession(none) should be empty, got %+v", other)
	}
}

func TestPendingQueueRemoveBySession(t *testing.T) {
	q := NewPendingQueue()
	q.Add(PermissionRequest{ID: "1", SessionID: "lumen/P1"})
	q.Add(PermissionRequest{ID: "2", SessionID: "lumen/P2"})
	q.Add(PermissionRequest{ID: "3", SessionID: "lumen/P1"})

	q.RemoveBySession("lumen/P1")

	if q.Len() != 1 {
		t.Fatalf("Len after RemoveBySession = %d, want 1", q.Len())
	}
	if left := q.BySession("lumen/P2"); len(left) != 1 || left[0].ID != "2" {
		t.Fatalf("unrelated session entries must survive, got %+v", left)
	}
	if gone := q.BySession("lumen/P1"); len(gone) != 0 {
		t.Fatalf("removed session entries still present: %+v", gone)
	}

	// Removing a session with no entries is a no-op.
	q.RemoveBySession("none")
	if q.Len() != 1 {
		t.Fatalf("RemoveBySession(none) changed queue, Len = %d, want 1", q.Len())
	}
}

func TestPendingQueueClear(t *testing.T) {
	q := NewPendingQueue()
	q.Add(PermissionRequest{ID: "a"})
	q.Add(PermissionRequest{ID: "b"})
	q.Clear()
	if q.Len() != 0 {
		t.Fatalf("Clear should empty queue")
	}
}

func TestPendingQueueLen(t *testing.T) {
	q := NewPendingQueue()
	if q.Len() != 0 {
		t.Fatalf("empty queue Len = %d, want 0", q.Len())
	}
	q.Add(PermissionRequest{ID: "a"})
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1", q.Len())
	}
}

func TestPendingQueueConcurrent(t *testing.T) {
	q := NewPendingQueue()
	var wg sync.WaitGroup
	const n = 100
	for i := 0; i < n; i++ {
		wg.Add(3)
		id := fmt.Sprintf("req-%d", i)
		go func(id string) {
			defer wg.Done()
			q.Add(PermissionRequest{ID: id, SessionID: "s"})
		}(id)
		go func() {
			defer wg.Done()
			_ = q.GetAll()
		}()
		go func() {
			defer wg.Done()
			_ = q.Len()
		}()
	}
	wg.Wait()
	if q.Len() != n {
		t.Fatalf("Len = %d, want %d", q.Len(), n)
	}

	// Remove all concurrently.
	for i := 0; i < n; i++ {
		wg.Add(1)
		id := fmt.Sprintf("req-%d", i)
		go func(id string) {
			defer wg.Done()
			q.Remove(id)
		}(id)
	}
	wg.Wait()
	if q.Len() != 0 {
		t.Fatalf("after concurrent removes Len = %d, want 0", q.Len())
	}
}
