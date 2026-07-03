// fakeworker is a scripted double of an OpenAI-compatible worker endpoint
// (MIXED-TASKS.md MP-07), the mixed-programming counterpart of fakeclaude.
// It serves POST /chat/completions with deterministic SSE responses from a
// JSON scenario (testdata/worker-scenarios/*.json) — no real model, no API
// tokens spent.
//
// Usage:
//
//	fakeworker -scenario testdata/worker-scenarios/clean-round1.json [-addr 127.0.0.1:0]
//
// The scenario path may also come from the FAKEWORKER_SCENARIO env var. On
// start the bound address is printed to stdout as FAKEWORKER_URL=http://...,
// ready to paste into a worker's base_url.
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	"claude-manager/internal/testkit"
)

func main() {
	srv, ln, err := start(os.Args[1:], os.Getenv("FAKEWORKER_SCENARIO"), os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeworker:", err)
		os.Exit(1)
	}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "fakeworker:", err)
		os.Exit(1)
	}
}

// start parses flags, loads the scenario and binds the listener. Split from
// main so tests can run the full server lifecycle in-process.
func start(args []string, scenarioEnv string, out io.Writer) (*http.Server, net.Listener, error) {
	fs := flag.NewFlagSet("fakeworker", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "listen address")
	scenarioPath := fs.String("scenario", "", "path to a worker scenario JSON (default: $FAKEWORKER_SCENARIO)")
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}

	path := *scenarioPath
	if path == "" {
		path = scenarioEnv
	}
	if path == "" {
		return nil, nil, fmt.Errorf("no scenario: pass -scenario or set FAKEWORKER_SCENARIO")
	}
	scenario, err := testkit.LoadWorkerScenario(path)
	if err != nil {
		return nil, nil, fmt.Errorf("load scenario: %w", err)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen %s: %w", *addr, err)
	}
	fmt.Fprintf(out, "FAKEWORKER_URL=http://%s\n", ln.Addr())

	return &http.Server{Handler: testkit.NewFakeWorker(scenario)}, ln, nil
}
