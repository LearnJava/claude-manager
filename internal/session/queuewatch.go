package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"claude-manager/internal/fsutil"
)

// EventNameQueueChanged tells the UI that a session's queue file, or a task
// file one of its pointers refers to, changed on disk (QUEUE-TASKS.md QV-02).
const EventNameQueueChanged = "session:queue_changed"

// QueueChangedEvent is the payload of EventNameQueueChanged.
type QueueChangedEvent struct {
	ID string `json:"id"`
}

// queueWatchInterval is how often queue files are polled; a var so tests can
// shorten it.
var queueWatchInterval = 2 * time.Second

// queueWatch polls the queue files of every configured session with a
// task_source. Only os.Stat on the pointed-at task files; the (small) queue
// file itself is read to learn its pointers. No fsnotify: polling is enough.
type queueWatch struct {
	m    *SessionManager
	prev map[string]string // session ID -> signature of the last pass
}

// StartQueueWatch launches the polling goroutine once; it stops on Shutdown.
func (m *SessionManager) StartQueueWatch() {
	m.mu.Lock()
	if m.queueWatchCancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.queueWatchCancel = cancel
	m.mu.Unlock()

	w := &queueWatch{m: m, prev: make(map[string]string)}
	go func() {
		t := time.NewTicker(queueWatchInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.tick()
			}
		}
	}()
}

// stopQueueWatch ends the polling goroutine, if running.
func (m *SessionManager) stopQueueWatch() {
	m.mu.Lock()
	cancel := m.queueWatchCancel
	m.queueWatchCancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// tick runs one pass. The first pass for a session only records its state.
func (w *queueWatch) tick() {
	type target struct{ id, root, source string }
	var targets []target
	w.m.mu.Lock()
	if w.m.cfg != nil {
		for i := range w.m.cfg.Projects {
			p := &w.m.cfg.Projects[i]
			for _, s := range p.Sessions {
				if ts := strings.TrimSpace(s.TaskSource); ts != "" && p.Path != "" {
					targets = append(targets, target{sessionID(p.Name, s.Name), p.Path, ts})
				}
			}
		}
	}
	w.m.mu.Unlock()

	seen := make(map[string]bool, len(targets))
	for _, t := range targets {
		seen[t.id] = true
		sig := queueSignature(t.root, t.source)
		old, known := w.prev[t.id]
		w.prev[t.id] = sig
		if known && old != sig {
			w.m.emit(EventNameQueueChanged, QueueChangedEvent{ID: t.id})
		}
	}
	for id := range w.prev {
		if !seen[id] {
			delete(w.prev, id)
		}
	}
}

// queueSignature fingerprints the queue file and every file its pointers
// name as (path, mtime, size); a missing file is part of the fingerprint.
func queueSignature(root, source string) string {
	resolve := func(p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		return fsutil.ResolveCaseInsensitive(root, p)
	}
	queuePath := resolve(source)
	paths := map[string]bool{queuePath: true}
	if data, err := os.ReadFile(queuePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			s := strings.TrimSpace(line)
			if mm := taskPointerCapture.FindStringSubmatch(s); mm != nil && !strings.HasPrefix(s, "#") {
				paths[resolve(mm[1])] = true
			}
		}
	}
	keys := make([]string, 0, len(paths))
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, p := range keys {
		if fi, err := os.Stat(p); err != nil {
			fmt.Fprintf(&b, "%s|missing;", p)
		} else {
			fmt.Fprintf(&b, "%s|%d|%d;", p, fi.ModTime().UnixNano(), fi.Size())
		}
	}
	return b.String()
}
