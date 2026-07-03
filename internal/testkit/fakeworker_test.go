package testkit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScenarioFile(t *testing.T, sc WorkerScenario) string {
	t.Helper()
	data, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadWorkerScenario(t *testing.T) {
	path := writeScenarioFile(t, WorkerScenario{
		Name:      "s",
		Responses: []WorkerResponse{{Content: "hi"}},
	})
	sc, err := LoadWorkerScenario(path)
	if err != nil {
		t.Fatalf("LoadWorkerScenario: %v", err)
	}
	if sc.Name != "s" || len(sc.Responses) != 1 {
		t.Fatalf("unexpected scenario: %+v", sc)
	}
}

func TestLoadWorkerScenario_Invalid(t *testing.T) {
	cases := map[string]WorkerScenario{
		"no name":       {Responses: []WorkerResponse{{Content: "x"}}},
		"no responses":  {Name: "s"},
		"bad status":    {Name: "s", Responses: []WorkerResponse{{Status: 302}}},
		"bad finish":    {Name: "s", Responses: []WorkerResponse{{FinishReason: "eof"}}},
		"neg repeat":    {Name: "s", Responses: []WorkerResponse{{Repeat: -1}}},
		"neg chunksize": {Name: "s", Responses: []WorkerResponse{{ChunkSize: -1}}},
	}
	for name, sc := range cases {
		if _, err := LoadWorkerScenario(writeScenarioFile(t, sc)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestLoadWorkerScenario_MissingFile(t *testing.T) {
	if _, err := LoadWorkerScenario(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// postChat sends a minimal chat-completions request and returns the response.
func postChat(t *testing.T, url string) *http.Response {
	t.Helper()
	body := `{"model":"fake/model","messages":[{"role":"user","content":"do it"}],"stream":true}`
	resp, err := http.Post(url+"/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// readSSE reassembles delta content and the final finish_reason from an SSE body.
func readSSE(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	defer resp.Body.Close()
	var content strings.Builder
	finish := ""
	sawDone := false
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			sawDone = true
			break
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("bad chunk %q: %v", data, err)
		}
		for _, c := range chunk.Choices {
			content.WriteString(c.Delta.Content)
			if c.FinishReason != nil {
				finish = *c.FinishReason
			}
		}
	}
	if !sawDone {
		t.Fatal("stream did not end with data: [DONE]")
	}
	return content.String(), finish
}

func TestFakeWorker_SSEResponse(t *testing.T) {
	fw := NewFakeWorker(&WorkerScenario{
		Name:      "s",
		Responses: []WorkerResponse{{Content: "hello world", FinishReason: "stop", ChunkSize: 3}},
	})
	srv := httptest.NewServer(fw)
	defer srv.Close()

	resp := postChat(t, srv.URL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	content, finish := readSSE(t, resp)
	if content != "hello world" || finish != "stop" {
		t.Fatalf("content=%q finish=%q", content, finish)
	}

	reqs := fw.Requests()
	if len(reqs) != 1 || reqs[0].Model != "fake/model" || len(reqs[0].Messages) != 1 {
		t.Fatalf("recorded requests = %+v", reqs)
	}
	if reqs[0].Messages[0].Content != "do it" {
		t.Errorf("message content = %q", reqs[0].Messages[0].Content)
	}
}

func TestFakeWorker_DefaultFinishReasonIsStop(t *testing.T) {
	fw := NewFakeWorker(&WorkerScenario{Name: "s", Responses: []WorkerResponse{{Content: "x"}}})
	srv := httptest.NewServer(fw)
	defer srv.Close()

	_, finish := readSSE(t, postChat(t, srv.URL))
	if finish != "stop" {
		t.Fatalf("finish = %q, want stop", finish)
	}
}

func TestFakeWorker_ErrorStatusWithRepeat(t *testing.T) {
	fw := NewFakeWorker(&WorkerScenario{
		Name: "storm",
		Responses: []WorkerResponse{
			{Status: 429, Body: "slow down", Repeat: 2},
			{Content: "ok"},
		},
	})
	srv := httptest.NewServer(fw)
	defer srv.Close()

	for i := 0; i < 2; i++ {
		resp := postChat(t, srv.URL)
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 429 || !strings.Contains(b.String(), "slow down") {
			t.Fatalf("request %d: status=%d body=%q", i, resp.StatusCode, b.String())
		}
	}
	content, _ := readSSE(t, postChat(t, srv.URL))
	if content != "ok" {
		t.Fatalf("content = %q", content)
	}
	if n := len(fw.Requests()); n != 3 {
		t.Fatalf("requests = %d, want 3", n)
	}
}

func TestFakeWorker_Exhausted(t *testing.T) {
	fw := NewFakeWorker(&WorkerScenario{Name: "s", Responses: []WorkerResponse{{Content: "x"}}})
	srv := httptest.NewServer(fw)
	defer srv.Close()

	readSSE(t, postChat(t, srv.URL)) // consume the only response

	resp := postChat(t, srv.URL)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestFakeWorker_RejectsWrongRoute(t *testing.T) {
	fw := NewFakeWorker(&WorkerScenario{Name: "s", Responses: []WorkerResponse{{Content: "x"}}})
	srv := httptest.NewServer(fw)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET status = %d, want 404", resp.StatusCode)
	}
}

func TestFakeWorker_BadRequestBody(t *testing.T) {
	fw := NewFakeWorker(&WorkerScenario{Name: "s", Responses: []WorkerResponse{{Content: "x"}}})
	srv := httptest.NewServer(fw)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/chat/completions", "application/json", strings.NewReader("{broken"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSplitRunes(t *testing.T) {
	cases := []struct {
		s    string
		size int
		want []string
	}{
		{"", 3, nil},
		{"abc", 0, []string{"abc"}},
		{"abcdef", 4, []string{"abcd", "ef"}},
		{"привет", 4, []string{"прив", "ет"}}, // rune-safe, not byte-safe
	}
	for _, c := range cases {
		got := splitRunes(c.s, c.size)
		if len(got) != len(c.want) {
			t.Errorf("splitRunes(%q,%d) = %v, want %v", c.s, c.size, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitRunes(%q,%d)[%d] = %q, want %q", c.s, c.size, i, got[i], c.want[i])
			}
		}
	}
}
