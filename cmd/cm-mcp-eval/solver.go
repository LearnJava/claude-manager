package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Solver is a scripted agent: it calls cm-mcp tools the way a model would,
// without a model. The offline test uses it to prove every task is solvable
// through the tools and that its check passes after the right actions and
// fails before.
type Solver struct {
	cs *mcp.ClientSession
}

// ConnectSolver starts cm-mcp against the instance and connects an SDK client.
func ConnectSolver(ctx context.Context, cmMCP string, in *Instance, level string) (*Solver, error) {
	cmd := exec.Command(cmMCP, "-level", level)
	cmd.Env = append(os.Environ(), "CM_CONTROL_ADDR="+in.Addr, "CM_CONTROL_TOKEN="+in.Token)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "cm-mcp-eval", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, err
	}
	return &Solver{cs: cs}, nil
}

// Close ends the connection.
func (s *Solver) Close() { _ = s.cs.Close() }

// Call invokes a tool and returns its structured content; a tool error is an
// error carrying the message the agent would have seen.
func (s *Solver) Call(name string, args map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	res, err := s.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if res.IsError {
		var b strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		return nil, fmt.Errorf("%s: %s", name, b.String())
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

// Must is Call that records the first error and turns later calls into no-ops.
type Steps struct {
	s   *Solver
	err error
}

// Steps wraps the solver for a straight-line script.
func (s *Solver) Steps() *Steps { return &Steps{s: s} }

// Do calls a tool unless an earlier step failed.
func (st *Steps) Do(name string, args map[string]any) map[string]any {
	if st.err != nil {
		return nil
	}
	out, err := st.s.Call(name, args)
	st.err = err
	return out
}

// Err is the first failure.
func (st *Steps) Err() error { return st.err }

// Poll repeats a tool call until pred accepts its result, or 20 s pass.
func (st *Steps) Poll(name string, args map[string]any, pred func(map[string]any) bool) {
	deadline := time.Now().Add(20 * time.Second)
	for st.err == nil {
		out := st.Do(name, args)
		if st.err != nil {
			return
		}
		if pred(out) {
			return
		}
		if time.Now().After(deadline) {
			raw, _ := json.Marshal(out)
			st.err = fmt.Errorf("%s: condition not reached; last: %.300s", name, raw)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sessionStatus digs the status out of a get_session result.
func sessionStatus(out map[string]any) string {
	if sess, ok := out["session"].(map[string]any); ok {
		if s, _ := sess["status"].(string); s != "" {
			return s
		}
	}
	s, _ := out["status"].(string)
	return s
}
