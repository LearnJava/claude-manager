package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"claude-manager/docs"
	"claude-manager/internal/control"
)

const (
	mimeJSON = "application/json"
	mimeMD   = "text/markdown"
)

func noArgs(string) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

// addResources registers the read-only resources. Every body comes from the
// same tools an agent could call, shaped by the same code, so a resource never
// shows more than a read-level tool does (no token, no secrets).
func (s *mcpServer) addResources(srv *mcp.Server) {
	srv.AddResource(&mcp.Resource{
		URI: "cm://guide", Name: "guide", Title: "Claude Manager agent guide", MIMEType: mimeMD,
		Description: "How to drive Claude Manager through this server: model, access levels, identifiers, tool areas, waiting and errors.",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return textResource(req.Params.URI, mimeMD, docs.MCPGuide), nil
	})
	srv.AddResource(&mcp.Resource{
		URI: "cm://projects", Name: "projects", Title: "Projects", MIMEType: mimeJSON,
		Description: "The configured projects with folder, session names and mirror and mixed-programming flags (first page; same data as get_projects).",
	}, s.readTool("get_projects", noArgs))
	srv.AddResource(&mcp.Resource{
		URI: "cm://sessions", Name: "sessions", Title: "Sessions", MIMEType: mimeJSON,
		Description: "All sessions with status and metrics (first page; same data as get_sessions).",
	}, s.readTool("get_sessions", noArgs))

	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "cm://projects/{project}/config", Name: "project-config", Title: "Project config", MIMEType: mimeJSON,
		Description: "One project's settings and its sessions, in config.toml shape, without secrets.",
	}, s.readProjectConfig)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "cm://sessions/{project}/{session}", Name: "session", Title: "Session", MIMEType: mimeJSON,
		Description: "One session's full state: status, current task, pending questions and permissions (same data as get_session).",
	}, s.readTool("get_session", func(uri string) (json.RawMessage, error) {
		project, session, _, err := sessionURI(uri)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"session_id": project + "/" + session})
	}))
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "cm://sessions/{project}/{session}/log{?cursor}", Name: "session-log", Title: "Session log", MIMEType: mimeJSON,
		Description: "A page of a session's log, oldest first; pass the next_cursor of the previous page as ?cursor= (same data as get_session_logs).",
	}, s.readTool("get_session_logs", func(uri string) (json.RawMessage, error) {
		project, session, query, err := sessionURI(uri)
		if err != nil {
			return nil, err
		}
		args := map[string]string{"session_id": project + "/" + session}
		if c := query.Get("cursor"); c != "" {
			args["cursor"] = c
		}
		return json.Marshal(args)
	}))
}

// readTool serves a resource from a tool call whose arguments come from the URI.
func (s *mcpServer) readTool(tool string, argsFor func(uri string) (json.RawMessage, error)) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		args, err := argsFor(uri)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		out, err := s.shapedTool(ctx, req.Session, tool, args)
		if err != nil {
			return nil, err
		}
		return jsonResource(uri, out)
	}
}

// shapedTool runs a tool the way tools/call does and returns its structured output.
func (s *mcpServer) shapedTool(ctx context.Context, ss *mcp.ServerSession, tool string, args json.RawMessage) (any, error) {
	ctx = withClient(ctx, ss)
	raw, err := s.callToolCtx(ctx, tool, args)
	if err == nil {
		var out any
		if out, _, err = control.ShapeResult(tool, args, raw); err == nil {
			return out, nil
		}
	}
	// A resource read has no isError result: report a JSON-RPC internal error
	// (with the hint, e.g. "start Claude Manager") instead of the SDK's code 0.
	return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: err.Error()}
}

func (s *mcpServer) readProjectConfig(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	rest, ok := strings.CutPrefix(uri, "cm://projects/")
	name, ok2 := strings.CutSuffix(rest, "/config")
	name, err := url.PathUnescape(name)
	if !ok || !ok2 || err != nil || name == "" || strings.Contains(name, "/") {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	out, err := s.shapedTool(ctx, req.Session, "get_config", json.RawMessage(`{"section":"project"}`))
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(out)
	var shaped struct {
		Version string         `json:"version"`
		Config  map[string]any `json:"config"`
	}
	_ = json.Unmarshal(b, &shaped)
	projects, _ := shaped.Config["project"].([]any)
	for _, p := range projects {
		if m, _ := p.(map[string]any); m != nil && m["name"] == name {
			return jsonResource(uri, map[string]any{"version": shaped.Version, "project": m})
		}
	}
	return nil, mcp.ResourceNotFoundError(uri)
}

// sessionURI splits cm://sessions/{project}/{session}[/log][?query].
func sessionURI(uri string) (project, session string, query url.Values, err error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "cm" || u.Host != "sessions" {
		return "", "", nil, fmt.Errorf("not a session resource: %s", uri)
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) == 3 && parts[2] == "log" {
		parts = parts[:2]
	}
	if len(parts) != 2 {
		return "", "", nil, fmt.Errorf("not a session resource: %s", uri)
	}
	if project, err = url.PathUnescape(parts[0]); err != nil {
		return "", "", nil, err
	}
	if session, err = url.PathUnescape(parts[1]); err != nil {
		return "", "", nil, err
	}
	if project == "" || session == "" {
		return "", "", nil, fmt.Errorf("not a session resource: %s", uri)
	}
	return project, session, u.Query(), nil
}

func textResource(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: text}}}
}

func jsonResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return textResource(uri, mimeJSON, string(b)), nil
}
