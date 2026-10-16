package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// AccessLevel is the cm-mcp access tier a tool belongs to (docs/mcp.md,
// "Уровни доступа"). The tier is declared here; filtering by it is MCP-05.
type AccessLevel string

const (
	LevelRead    AccessLevel = "read"
	LevelOperate AccessLevel = "operate"
	LevelAdmin   AccessLevel = "admin"
)

// ToolAnnotations mirrors the MCP tool annotations. control must not import
// the MCP SDK (the GUI binary links this package), so cm-mcp converts it.
type ToolAnnotations struct {
	ReadOnly    bool `json:"readOnlyHint"`
	Destructive bool `json:"destructiveHint"`
	Idempotent  bool `json:"idempotentHint"`
	OpenWorld   bool `json:"openWorldHint"`
}

// Doc is the structured description of a tool, rendered in one fixed order
// (docs/mcp.md, "Стандарт описаний"); the registry linter checks the result.
type Doc struct {
	What    string // one sentence: what the tool does
	UseWhen string // when to call it
	NotFor  string // when not to, and which tool to use instead
	Returns string // what comes back
	Effects string // side effects, cost, "none, read only"
	Example string // example arguments as JSON
}

func (d Doc) render(level AccessLevel) string {
	return fmt.Sprintf("%s Use when: %s Not for: %s Returns: %s Side effects: %s Access: %s. Example: %s",
		d.What, d.UseWhen, d.NotFor, d.Returns, d.Effects, level, d.Example)
}

// Tool is one MCP tool, declared exactly once: schemas come from the Go
// input/output structs, the handler is a translation into a control-plane
// call plus a shaping of its answer.
type Tool struct {
	Name        string
	Title       string
	Description string
	Level       AccessLevel
	Annotations ToolAnnotations
	// Covers lists the exported *App methods this tool reaches; the parity
	// test (parity_test.go) checks every App method against these.
	Covers       []string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage

	translate func(args json.RawMessage) (endpoint string, body any, err error)
	shape     func(args json.RawMessage, raw any) (out any, summary string, err error)
}

// spec is the typed declaration define turns into a Tool.
type spec[In, Out any] struct {
	Name, Title string
	Doc         Doc
	Level       AccessLevel
	Annotations ToolAnnotations
	Covers      []string
	// Input refines the generated input schema: enums, defaults, bounds.
	Input func(props map[string]*jsonschema.Schema)
	// Translate maps decoded arguments to a control-plane call.
	Translate func(in In) (endpoint string, body any, err error)
	// Result builds the structured output from the control-plane result.
	Result func(in In, raw any) Out
	// Summary is the short text shown next to structuredContent (not a JSON dump).
	Summary func(in In, out Out) string
}

func define[In, Out any](s spec[In, Out]) Tool {
	return Tool{
		Name: s.Name, Title: s.Title, Description: s.Doc.render(s.Level),
		Level: s.Level, Annotations: s.Annotations, Covers: s.Covers,
		InputSchema:  mustSchema[In](s.Input),
		OutputSchema: mustSchema[Out](nil),
		translate: func(args json.RawMessage) (string, any, error) {
			in, err := decodeArgs[In](args)
			if err != nil {
				return "", nil, err
			}
			return s.Translate(in)
		},
		shape: func(args json.RawMessage, raw any) (any, string, error) {
			in, err := decodeArgs[In](args)
			if err != nil {
				return nil, "", err
			}
			out := s.Result(in, raw)
			return out, s.Summary(in, out), nil
		},
	}
}

func decodeArgs[In any](args json.RawMessage) (In, error) {
	var in In
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return in, fmt.Errorf("bad arguments: %w", err)
	}
	return in, nil
}

func mustSchema[T any](refine func(props map[string]*jsonschema.Schema)) json.RawMessage {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("mcp tool schema: %v", err))
	}
	if refine != nil {
		refine(s.Properties)
	}
	b, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("mcp tool schema: %v", err))
	}
	return b
}

// MCPTools is the ordered registry of all tools exposed via cm-mcp.
// Registration: claude mcp add cm -- cm-mcp
var MCPTools = buildMCPTools()

// TranslateTool converts a tools/call invocation into a control-plane HTTP
// request. Returns the endpoint ("/rpc" or "/wait") and a JSON-serialisable
// request body. Returns an error for unknown tools or bad arguments.
func TranslateTool(name string, args json.RawMessage) (endpoint string, body any, err error) {
	t := FindTool(name)
	if t == nil {
		return "", nil, fmt.Errorf("unknown tool: %s", name)
	}
	return t.translate(args)
}

// ShapeResult turns the control-plane result of tool name into the
// structuredContent object (valid against the tool's OutputSchema) and a
// short text summary for content.
func ShapeResult(name string, args json.RawMessage, raw any) (out any, summary string, err error) {
	t := FindTool(name)
	if t == nil {
		return nil, "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.shape(args, raw)
}

// FindTool returns the registry entry for name, or nil.
func FindTool(name string) *Tool {
	for i := range MCPTools {
		if MCPTools[i].Name == name {
			return &MCPTools[i]
		}
	}
	return nil
}

// rpcCallBody builds a JSON-RPC 2.0 request body for POST /rpc.
func rpcCallBody(method string, params any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}
}

// waitCallBody builds a request body for POST /wait.
func waitCallBody(event string, match any, timeoutMs int64) map[string]any {
	return map[string]any{
		"event":      event,
		"match":      match,
		"timeout_ms": timeoutMs,
	}
}

// withSince adds since_seq to a /wait body when the caller gave one.
func withSince(body map[string]any, since *int64) map[string]any {
	if since != nil && *since >= 0 {
		body["since_seq"] = *since
	}
	return body
}

// longRunning are the tools that block for a long time: the wait_* family
// and the ones that run a model. cm-mcp sends notifications/progress for them
// and bounds the model-running ones by LongToolTimeout.
var longRunning = map[string]bool{
	"wait_for_status": true, "wait_for_event": true, "wait_for_worker_status": true, "wait_for_any": true,
	"run_preflight": true, "session_preflight": true, "generate_roadmap": true, "distill_skill": true,
}

// IsLongRunning reports whether tool name may block for minutes.
func IsLongRunning(name string) bool { return longRunning[name] }

// IsWaitTool reports whether the tool is a wait_* call, which the control-plane
// bounds itself (MaxWaitTimeout).
func IsWaitTool(name string) bool { return strings.HasPrefix(name, "wait_for_") }

// LongToolTimeout is the upper bound for a model-running tool call.
const LongToolTimeout = 15 * time.Minute
