package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/mcp/tools.golden.json")

const goldenPath = "../../testdata/mcp/tools.golden.json"

// TestToolsListGolden pins the tools/list contract (names, schemas,
// annotations, access levels). An intended change is committed with
// `go test ./cmd/cm-mcp -run TestToolsListGolden -update`.
func TestToolsListGolden(t *testing.T) {
	p := startPeer(t, newTestMCPServer(t, &stubCP{}))
	p.handshake("2025-11-25")
	p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := p.result(2)["tools"]
	got, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("%v (create it with -update)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatal("tools/list differs from testdata/mcp/tools.golden.json; if the change is intended, rerun with -update")
	}
}

func TestToolCall_StructuredContentAndSummary(t *testing.T) {
	stub := &stubCP{response: `{"jsonrpc":"2.0","id":1,"result":[{"id":"p/s"}]}`}
	p := startPeer(t, newTestMCPServer(t, stub))
	p.handshake("2025-11-25")
	p.send(callLine(2, "get_sessions"))
	r := p.result(2)
	sc, _ := r["structuredContent"].(map[string]any)
	if l, _ := sc["sessions"].([]any); len(l) != 1 {
		t.Fatalf("structuredContent: %v", r)
	}
	c, _ := r["content"].([]any)
	text, _ := c[0].(map[string]any)["text"].(string)
	if text != "1 session." {
		t.Fatalf("summary: %q", text)
	}
}
