package control

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func decodeWait(t *testing.T, rr interface{ Result() *http.Response }) waitResponse {
	t.Helper()
	var out waitResponse
	if err := json.NewDecoder(rr.Result().Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// An idle in the buffer from before send_message must not answer a wait that
// carries the seq send_message returned.
func TestWaitStatus_SinceSeqSkipsStaleIdle(t *testing.T) {
	srv, em := waitServer(&attnManager{statuses: map[string]string{"p/s": "working"}})
	em.Emit("session:status", map[string]string{"id": "p/s", "status": "idle"})
	since := em.Seq()

	done := make(chan waitResponse, 1)
	go func() {
		rr := doWait(srv, fmt.Sprintf(`{"event":"session:status","match":{"id":"p/s","status":"idle"},"status_now":true,"since_seq":%d,"timeout_ms":2000}`, since))
		done <- decodeWait(t, rr)
	}()
	select {
	case out := <-done:
		t.Fatalf("returned on the stale idle: %+v", out)
	case <-time.After(80 * time.Millisecond):
	}
	em.Emit("session:status", map[string]string{"id": "p/s", "status": "idle"})
	select {
	case out := <-done:
		if !out.Reached || out.Seq != since+1 {
			t.Fatalf("want the new idle with seq %d, got %+v", since+1, out)
		}
	case <-time.After(time.Second):
		t.Fatal("new idle not delivered")
	}
}

func TestWaitStatus_SinceSeqTakesNewerBufferedEvent(t *testing.T) {
	srv, em := waitServer(&attnManager{})
	since := em.Seq()
	em.Emit("session:status", map[string]string{"id": "p/s", "status": "idle"})
	rr := doWait(srv, fmt.Sprintf(`{"event":"session:status","match":{"id":"p/s","status":"idle"},"status_now":true,"since_seq":%d,"timeout_ms":100}`, since))
	if out := decodeWait(t, rr); !out.Reached || out.Seq != 1 {
		t.Fatalf("want buffered event seq 1, got %+v", out)
	}
}

func TestWaitStatus_NoSinceUsesCurrentStatus(t *testing.T) {
	srv, em := waitServer(&attnManager{statuses: map[string]string{"p/s": "idle"}})
	em.Emit("session:log", map[string]string{"id": "p/s"})
	rr := doWait(srv, `{"event":"session:status","match":{"id":"p/s","status":"idle"},"status_now":true,"timeout_ms":3000}`)
	out := decodeWait(t, rr)
	if rr.Code != http.StatusOK || !out.Reached || out.Seq != em.Seq() {
		t.Fatalf("want immediate reached at current seq, got %d %+v", rr.Code, out)
	}
}

func TestWaitStatus_NoSinceIgnoresBufferWhenNotCurrent(t *testing.T) {
	srv, em := waitServer(&attnManager{statuses: map[string]string{"p/s": "working"}})
	em.Emit("session:status", map[string]string{"id": "p/s", "status": "idle"}) // stale
	rr := doWait(srv, `{"event":"session:status","match":{"id":"p/s","status":"idle"},"status_now":true,"timeout_ms":40}`)
	if rr.Code != http.StatusRequestTimeout {
		t.Fatalf("a stale buffered idle must not count, got %d", rr.Code)
	}
}

func TestWaitEvent_AcceptsNewEvents(t *testing.T) {
	srv, em := waitServer(&attnManager{})
	done := make(chan waitResponse, 1)
	go func() {
		done <- decodeWait(t, doWait(srv, `{"event":"alert:budget","timeout_ms":2000}`))
	}()
	time.Sleep(50 * time.Millisecond)
	em.Emit("alert:budget", map[string]any{"kind": "daily"})
	select {
	case out := <-done:
		if !out.Reached || out.Event != "alert:budget" {
			t.Fatalf("got %+v", out)
		}
	case <-time.After(time.Second):
		t.Fatal("alert:budget not delivered")
	}
	var enum struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(FindTool("wait_for_event").InputSchema, &enum)
	if !strings.Contains(strings.Join(enum.Properties["event"].Enum, ","), "alert:budget") {
		t.Fatal("wait_for_event enum lacks alert:budget")
	}
}

// A burst of session:log must not push the awaited event out of the waiter's
// 64-slot buffer.
func TestWait_LogBurstDoesNotStarveWaiter(t *testing.T) {
	srv, em := waitServer(&attnManager{statuses: map[string]string{"p/s": "working"}})
	done := make(chan waitResponse, 1)
	go func() {
		done <- decodeWait(t, doWait(srv, `{"event":"session:status","match":{"id":"p/s","status":"idle"},"timeout_ms":3000}`))
	}()
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 200; i++ {
		em.Emit("session:log", map[string]any{"id": "p/s", "n": i})
	}
	em.Emit("session:status", map[string]string{"id": "p/s", "status": "idle"})
	select {
	case out := <-done:
		if !out.Reached || out.Event != "session:status" {
			t.Fatalf("got %+v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter starved by the log burst")
	}
}

func TestEmitter_SeqMonotonic(t *testing.T) {
	em := NewControlEmitter(5)
	for i := 1; i <= 3; i++ {
		em.Emit("session:log", i)
	}
	rec := em.Recent("", 0)
	if len(rec) != 3 || rec[0].Seq != 1 || rec[2].Seq != 3 || em.Seq() != 3 {
		t.Fatalf("seq not monotonic: %+v", rec)
	}
}

func TestSeqOfActionResult(t *testing.T) {
	out, _, err := ShapeResult("send_message", json.RawMessage(`{"session_id":"p/s","message":"hi"}`), map[string]any{"seq": float64(7)})
	if err != nil {
		t.Fatal(err)
	}
	if ack, ok := out.(sessionAck); !ok || ack.Seq == nil || *ack.Seq != 7 {
		t.Fatalf("ack must carry seq 7, got %+v", out)
	}
}

// Every event the GUI listens to is either in EventNames or explicitly
// excluded with a reason.
func TestEventNames_CoverFrontendEvents(t *testing.T) {
	excluded := map[string]string{
		"app:ready": "GUI-only startup signal, never emitted by a session or the control-plane",
	}
	re := regexp.MustCompile(`EventsOn\(\s*['"]([^'"]+)['"]`)
	root := filepath.Join("..", "..", "frontend", "src")
	found := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := filepath.Ext(p); ext != ".svelte" && ext != ".ts" && ext != ".js" {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, m := range re.FindAllSubmatch(b, -1) {
			found[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) < 10 {
		t.Fatalf("scan found only %d events: wrong path or pattern", len(found))
	}
	known := map[string]bool{}
	for _, e := range EventNames {
		known[e] = true
	}
	for name := range found {
		if !known[name] && excluded[name] == "" {
			t.Errorf("frontend listens to %q: add it to EventNames or exclude it with a reason", name)
		}
	}
}
