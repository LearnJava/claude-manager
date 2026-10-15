package control

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"claude-manager/internal/permission"
	"claude-manager/internal/session"
)

// Image limits for SendMessageWithImages (docs/mcp.md, "Сессии"): an image is
// given as a file path on the machine running the manager, or as base64.
const (
	MaxImages     = 4
	MaxImageBytes = 5 << 20 // decoded size of one image
)

var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

// SessionApp is the part of *App the session RPCs prefer over the bare manager:
// the GUI App remembers a model choice, persists a runtime and creates the
// ad-hoc chat session. A control server without it (playwright-server, tests)
// falls back to the manager where that is possible.
type SessionApp interface {
	StartSessionWithModel(project, name, model, effort string) error
	StartAdHocChatSession(project string) error
	StartProject(project string) error
	SetSessionModel(id, model string) error
	SetSessionRuntime(id, runtime string) error
}

type imageArg struct {
	MediaType  string `json:"media_type"`
	DataBase64 string `json:"data_base64"`
	Path       string `json:"path"`
}

// resolveImages validates the attachments and turns paths into base64.
func resolveImages(in []imageArg) ([]session.ImageAttachment, error) {
	if len(in) > MaxImages {
		return nil, fmt.Errorf("%d images given, at most %d are allowed per message: send the rest in a further message", len(in), MaxImages)
	}
	out := make([]session.ImageAttachment, 0, len(in))
	for i, im := range in {
		if (im.Path == "") == (im.DataBase64 == "") {
			return nil, fmt.Errorf("image %d: pass exactly one of path or data_base64", i)
		}
		var raw []byte
		mt := im.MediaType
		if im.Path != "" {
			want, ok := imageTypes[strings.ToLower(filepath.Ext(im.Path))]
			if !ok {
				return nil, fmt.Errorf("image %d: %q is not a png, jpg, gif or webp file", i, im.Path)
			}
			if mt == "" {
				mt = want
			}
			fi, err := os.Stat(im.Path)
			if err != nil {
				return nil, fmt.Errorf("image %d: cannot read %q: %v; pass an existing absolute path on the machine running Claude Manager", i, im.Path, err)
			}
			if fi.Size() > MaxImageBytes {
				return nil, fmt.Errorf("image %d: %q is %d bytes, over the %d byte limit: downscale it first", i, im.Path, fi.Size(), MaxImageBytes)
			}
			if raw, err = os.ReadFile(im.Path); err != nil {
				return nil, fmt.Errorf("image %d: cannot read %q: %v", i, im.Path, err)
			}
		} else {
			var err error
			if raw, err = base64.StdEncoding.DecodeString(im.DataBase64); err != nil {
				return nil, fmt.Errorf("image %d: data_base64 is not valid base64 (no data: prefix, standard alphabet): %v", i, err)
			}
			if len(raw) > MaxImageBytes {
				return nil, fmt.Errorf("image %d: %d bytes decoded, over the %d byte limit: downscale it first", i, len(raw), MaxImageBytes)
			}
			if mt == "" {
				return nil, fmt.Errorf("image %d: media_type is required with data_base64, e.g. image/png", i)
			}
		}
		if !strings.HasPrefix(mt, "image/") {
			return nil, fmt.Errorf("image %d: media_type %q must be image/png, image/jpeg, image/gif or image/webp", i, mt)
		}
		out = append(out, session.ImageAttachment{MediaType: mt, DataBase64: base64.StdEncoding.EncodeToString(raw)})
	}
	return out, nil
}

// registerSessionRPCs adds the session-area methods that are not plain
// manager calls (MCP-06).
func (s *Server) registerSessionRPCs(reg map[string]handler) {
	m := s.manager
	sapp, _ := s.app.(SessionApp)

	reg["StartSessionWithModel"] = func(p json.RawMessage) (any, error) {
		var a struct{ Project, Session, Model, Effort, Runtime string }
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if a.Runtime != "" {
			if sapp == nil {
				return nil, fmt.Errorf("choosing a runtime needs the Claude Manager app (not available in this control server)")
			}
			if err := sapp.SetSessionRuntime(a.Project+"/"+a.Session, a.Runtime); err != nil {
				return nil, err
			}
		}
		if sapp != nil {
			return nil, sapp.StartSessionWithModel(a.Project, a.Session, a.Model, a.Effort)
		}
		return nil, m.StartSessionWithOverride(a.Project, a.Session, a.Model, a.Effort)
	}

	reg["StartAdHocChatSession"] = func(p json.RawMessage) (any, error) {
		var a struct{ Project string }
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if sapp == nil {
			return nil, fmt.Errorf("StartAdHocChatSession needs the Claude Manager app (not available in this control server)")
		}
		return nil, sapp.StartAdHocChatSession(a.Project)
	}

	// StartProject prefers the App: it launches in cache-affinity order.
	if sapp != nil {
		reg["StartProject"] = func(p json.RawMessage) (any, error) {
			var a struct{ Project string }
			if err := json.Unmarshal(p, &a); err != nil {
				return nil, err
			}
			return nil, sapp.StartProject(a.Project)
		}
	}

	reg["SetSessionModel"] = func(p json.RawMessage) (any, error) {
		var a struct{ ID, Model string }
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if sapp != nil {
			return nil, sapp.SetSessionModel(a.ID, a.Model)
		}
		return nil, m.SetSessionModel(a.ID, a.Model)
	}

	reg["SetSessionRuntime"] = func(p json.RawMessage) (any, error) {
		var a struct{ ID, Runtime string }
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if sapp == nil {
			return nil, fmt.Errorf("SetSessionRuntime needs the Claude Manager app (not available in this control server)")
		}
		return nil, sapp.SetSessionRuntime(a.ID, a.Runtime)
	}

	reg["SendMessageWithImages"] = func(p json.RawMessage) (any, error) {
		var a struct {
			ID      string     `json:"id"`
			Message string     `json:"message"`
			Images  []imageArg `json:"images"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		imgs, err := resolveImages(a.Images)
		if err != nil {
			return nil, err
		}
		return nil, m.SendMessageWithImages(a.ID, a.Message, imgs)
	}

	// GetSessionOverview is what get_session returns: everything an agent needs
	// to decide what to do next, in one call.
	reg["GetSessionOverview"] = func(p json.RawMessage) (any, error) {
		var a struct{ ID string }
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		st, ok := m.GetSession(a.ID)
		if !ok {
			return nil, fmt.Errorf("session %q not found: list sessions with get_sessions", a.ID)
		}
		perms := []permission.PermissionRequest{}
		for _, r := range m.GetPendingPermissions() {
			if r.SessionID == a.ID {
				perms = append(perms, r)
			}
		}
		var question *session.PendingQuestion
		for _, q := range m.GetPendingQuestions() {
			if q.SessionID == a.ID {
				qq := q.Question
				question = &qq
			}
		}
		return map[string]any{
			"session":             st,
			"saved_state":         m.GetSessionState(st.Project, st.Name),
			"pending_permissions": perms,
			"pending_question":    question,
		}, nil
	}
}
