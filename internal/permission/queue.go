package permission

import "sync"

// PendingQueue is a thread-safe queue of permission requests waiting for a
// user decision. Requests are kept in insertion order and identified by their
// PermissionRequest.ID.
type PendingQueue struct {
	mu    sync.RWMutex
	items []PermissionRequest
}

// NewPendingQueue returns an empty queue.
func NewPendingQueue() *PendingQueue {
	return &PendingQueue{}
}

// Add appends req to the queue. If a request with the same ID already exists,
// it is replaced in place (preserving its position).
func (q *PendingQueue) Add(req PermissionRequest) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, existing := range q.items {
		if existing.ID == req.ID {
			q.items[i] = req
			return
		}
	}
	q.items = append(q.items, req)
}

// Remove deletes the request with the given id. Returns true if a request
// was removed, false if none matched.
func (q *PendingQueue) Remove(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, existing := range q.items {
		if existing.ID == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return true
		}
	}
	return false
}

// Get returns the request with the given id, if present.
func (q *PendingQueue) Get(id string) (PermissionRequest, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	for _, existing := range q.items {
		if existing.ID == id {
			return existing, true
		}
	}
	return PermissionRequest{}, false
}

// GetAll returns a copy of every pending request in insertion order.
func (q *PendingQueue) GetAll() []PermissionRequest {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]PermissionRequest, len(q.items))
	copy(out, q.items)
	return out
}

// BySession returns a copy of all pending requests belonging to sessionID.
func (q *PendingQueue) BySession(sessionID string) []PermissionRequest {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]PermissionRequest, 0)
	for _, existing := range q.items {
		if existing.SessionID == sessionID {
			out = append(out, existing)
		}
	}
	return out
}

// Len returns the number of pending requests.
func (q *PendingQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.items)
}

// RemoveBySession removes all pending requests belonging to sessionID.
func (q *PendingQueue) RemoveBySession(sessionID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.items[:0]
	for _, r := range q.items {
		if r.SessionID != sessionID {
			kept = append(kept, r)
		}
	}
	q.items = kept
}

// Clear empties the queue.
func (q *PendingQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
}
