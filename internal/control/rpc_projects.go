package control

import (
	"encoding/json"
	"fmt"
	"sort"

	"claude-manager/internal/config"
	"claude-manager/internal/envcheck"
)

// ProjectsApp is the part of *App the projects-and-configuration RPCs need
// beyond GetConfig/UpdateConfig: the actions on a project's folder and its
// mirror. A control server without it (playwright-server, tests) reports these
// methods as unavailable. The Mirror and Status methods return any because the
// App's own result types live in package main.
type ProjectsApp interface {
	CheckEnvironment() envcheck.Report
	HasClaudeMd(projectPath string) bool
	HasSessionProtocol(project string) (bool, error)
	InstallSessionProtocol(project string) ([]string, error)
	GenerateClaudeMdSession(project string) error
	InitGitRepo(projectPath string) error
	MirrorProjectStatus(project string) (any, error)
	UnmirrorProject(project string) error
	SyncProjectMirrorsResults() (any, error)
	MirrorStatusOf(project string) any
	ShipLogsNowResult() (any, error)
	LogShipStatusOf() any
}

type editArgs struct {
	Project string         `json:"project"`
	Session string         `json:"session"`
	Name    string         `json:"name"`
	Path    string         `json:"path"`
	Fields  map[string]any `json:"fields"`
	DryRun  bool           `json:"dry_run"`
}

// projectPath resolves a project name to its folder.
func (s *Server) projectPath(name string) (string, error) {
	if s.app == nil {
		return "", fmt.Errorf("config is not available in this control server")
	}
	c := s.app.GetConfig()
	if c == nil {
		return "", fmt.Errorf("no config loaded")
	}
	i := findProject(c, name)
	if i < 0 {
		return "", fmt.Errorf("project %q not found: configured projects are %s", name, cfgProjectNames(c))
	}
	return c.Projects[i].Path, nil
}

// activeSessions lists the sessions of project (all of it when session is
// empty, else that one) that have a process running.
func (s *Server) activeSessions(project, session string) []string {
	var out []string
	for _, st := range s.manager.GetAllSessions() {
		if st.Project != project || (session != "" && st.Name != session) {
			continue
		}
		if isActive(st.Status) {
			out = append(out, st.ID)
		}
	}
	sort.Strings(out)
	return out
}

// editConfig runs one point edit under the config lock: read the current
// config, apply op, and — unless dry — save through the same UpdateConfig the
// GUI uses. guard may refuse the edit before it is computed.
func (s *Server) editConfig(dry bool, op func(*config.AppConfig) (*ConfigEdit, error), guard func() error) (any, error) {
	if s.app == nil {
		return nil, fmt.Errorf("editing the config needs the Claude Manager app (not available in this control server)")
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	cur := s.app.GetConfig()
	if cur == nil {
		return nil, fmt.Errorf("no config loaded")
	}
	if guard != nil {
		if err := guard(); err != nil {
			return nil, err
		}
	}
	edit, err := op(cur)
	if err != nil {
		return nil, err
	}
	if !dry && len(edit.Changes) > 0 {
		if err := s.app.UpdateConfig(edit.Config); err != nil {
			return nil, err
		}
	}
	version, err := ConfigVersion(s.app.GetConfig())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"dry_run": dry, "changed": len(edit.Changes) > 0 && !dry,
		"changes": edit.Changes, "version": version,
	}, nil
}

func (s *Server) registerProjectRPCs(reg map[string]handler) {
	papp, _ := s.app.(ProjectsApp)
	needApp := func(what string) error {
		return fmt.Errorf("%s needs the Claude Manager app (not available in this control server)", what)
	}
	decode := func(p json.RawMessage) (editArgs, error) {
		var a editArgs
		if len(p) > 0 {
			if err := json.Unmarshal(p, &a); err != nil {
				return a, err
			}
		}
		return a, nil
	}
	// projectAction decodes {project}, checks the app and runs fn.
	projectAction := func(what string, fn func(a editArgs) (any, error)) handler {
		return func(p json.RawMessage) (any, error) {
			a, err := decode(p)
			if err != nil {
				return nil, err
			}
			if papp == nil {
				return nil, needApp(what)
			}
			if a.Project == "" {
				return nil, fmt.Errorf("project is required")
			}
			return fn(a)
		}
	}

	// ── Reads ────────────────────────────────────────────────────────────────

	reg["GetProjects"] = func(_ json.RawMessage) (any, error) {
		if s.app == nil {
			return nil, fmt.Errorf("config is not available in this control server")
		}
		out := []map[string]any{}
		if c := s.app.GetConfig(); c != nil {
			for _, p := range c.Projects {
				names := []string{}
				for _, ss := range p.Sessions {
					names = append(names, ss.Name)
				}
				out = append(out, map[string]any{
					"name": p.Name, "path": p.Path, "sessions": names,
					"mirror": p.Mirror, "mixed_programming": p.MixedProgramming,
				})
			}
		}
		return out, nil
	}

	reg["GetConfigView"] = func(p json.RawMessage) (any, error) {
		if s.app == nil || s.app.GetConfig() == nil {
			return nil, fmt.Errorf("no config loaded")
		}
		c := s.app.GetConfig()
		view, err := configView(c)
		if err != nil {
			return nil, err
		}
		version, err := ConfigVersion(c)
		if err != nil {
			return nil, err
		}
		return map[string]any{"version": version, "config": view}, nil
	}

	reg["CheckEnvironment"] = func(_ json.RawMessage) (any, error) {
		if papp == nil {
			return nil, needApp("the environment check")
		}
		return papp.CheckEnvironment(), nil
	}

	reg["HasClaudeMd"] = projectAction("the CLAUDE.md check", func(a editArgs) (any, error) {
		path, err := s.projectPath(a.Project)
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": a.Project, "path": path, "exists": papp.HasClaudeMd(path)}, nil
	})

	reg["HasSessionProtocol"] = projectAction("the session protocol check", func(a editArgs) (any, error) {
		ok, err := papp.HasSessionProtocol(a.Project)
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": a.Project, "installed": ok}, nil
	})

	reg["GetProjectMirror"] = projectAction("the mirror status", func(a editArgs) (any, error) {
		return papp.MirrorStatusOf(a.Project), nil
	})

	// GetProjectSetup answers the three "is it set up" questions at once.
	reg["GetProjectSetup"] = projectAction("the project setup check", func(a editArgs) (any, error) {
		path, err := s.projectPath(a.Project)
		if err != nil {
			return nil, err
		}
		protocol, err := papp.HasSessionProtocol(a.Project)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"project": a.Project, "path": path,
			"claude_md": papp.HasClaudeMd(path), "session_protocol": protocol,
			"mirror": papp.MirrorStatusOf(a.Project),
		}, nil
	})

	// ── Config edits (admin) ─────────────────────────────────────────────────

	reg["AddProject"] = func(p json.RawMessage) (any, error) {
		a, err := decode(p)
		if err != nil {
			return nil, err
		}
		return s.editConfig(a.DryRun, func(c *config.AppConfig) (*ConfigEdit, error) {
			return addProject(c, a.Name, a.Path)
		}, nil)
	}
	reg["RemoveProject"] = func(p json.RawMessage) (any, error) {
		a, err := decode(p)
		if err != nil {
			return nil, err
		}
		return s.editConfig(a.DryRun, func(c *config.AppConfig) (*ConfigEdit, error) {
			return removeProject(c, a.Name)
		}, func() error {
			if act := s.activeSessions(a.Name, ""); len(act) > 0 {
				return fmt.Errorf("project %q has running sessions (%v): stop them with stop_project first", a.Name, act)
			}
			return nil
		})
	}
	reg["UpdateProject"] = func(p json.RawMessage) (any, error) {
		a, err := decode(p)
		if err != nil {
			return nil, err
		}
		return s.editConfig(a.DryRun, func(c *config.AppConfig) (*ConfigEdit, error) {
			return updateProject(c, a.Project, a.Fields)
		}, nil)
	}
	reg["AddSession"] = func(p json.RawMessage) (any, error) {
		a, err := decode(p)
		if err != nil {
			return nil, err
		}
		return s.editConfig(a.DryRun, func(c *config.AppConfig) (*ConfigEdit, error) {
			return addSession(c, a.Project, a.Session, a.Fields)
		}, nil)
	}
	reg["RemoveSession"] = func(p json.RawMessage) (any, error) {
		a, err := decode(p)
		if err != nil {
			return nil, err
		}
		return s.editConfig(a.DryRun, func(c *config.AppConfig) (*ConfigEdit, error) {
			return removeSession(c, a.Project, a.Session)
		}, func() error {
			if act := s.activeSessions(a.Project, a.Session); len(act) > 0 {
				return fmt.Errorf("session %s/%s is running: stop it with stop_session first", a.Project, a.Session)
			}
			return nil
		})
	}
	reg["UpdateSession"] = func(p json.RawMessage) (any, error) {
		a, err := decode(p)
		if err != nil {
			return nil, err
		}
		return s.editConfig(a.DryRun, func(c *config.AppConfig) (*ConfigEdit, error) {
			return updateSession(c, a.Project, a.Session, a.Fields)
		}, nil)
	}
	reg["UpdateGlobalSettings"] = func(p json.RawMessage) (any, error) {
		a, err := decode(p)
		if err != nil {
			return nil, err
		}
		return s.editConfig(a.DryRun, func(c *config.AppConfig) (*ConfigEdit, error) {
			return updateGlobalSettings(c, a.Fields)
		}, nil)
	}

	// ReplaceConfig stores a whole config in its TOML shape (what get_config
	// returns) when version is still the current one.
	reg["ReplaceConfig"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Config  map[string]any `json:"config"`
			Version string         `json:"version"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if s.app == nil {
			return nil, needApp("replacing the config")
		}
		if a.Version == "" {
			return nil, fmt.Errorf("version is required: pass the version returned by get_config")
		}
		s.cfgMu.Lock()
		defer s.cfgMu.Unlock()
		cur, err := ConfigVersion(s.app.GetConfig())
		if err != nil {
			return nil, err
		}
		if cur != a.Version {
			return nil, fmt.Errorf("the config changed since version %s (now %s): call get_config again and redo the edit", a.Version, cur)
		}
		var next config.AppConfig
		if err := fromKeyed(normNumbers(a.Config).(map[string]any), &next); err != nil {
			return nil, fmt.Errorf("config has a value of the wrong type: %v", err)
		}
		if err := config.Validate(&next); err != nil {
			return nil, err
		}
		if err := s.app.UpdateConfig(next); err != nil {
			return nil, err
		}
		v, err := ConfigVersion(s.app.GetConfig())
		return map[string]any{"version": v}, err
	}

	// ── Project actions ──────────────────────────────────────────────────────

	reg["InitGitRepo"] = projectAction("git init", func(a editArgs) (any, error) {
		path, err := s.projectPath(a.Project)
		if err != nil {
			return nil, err
		}
		if err := CheckProjectDir(path); err != nil {
			return nil, err
		}
		return nil, papp.InitGitRepo(path)
	})
	reg["GenerateClaudeMdSession"] = projectAction("CLAUDE.md generation", func(a editArgs) (any, error) {
		return nil, papp.GenerateClaudeMdSession(a.Project)
	})
	reg["InstallSessionProtocol"] = projectAction("installing the session protocol", func(a editArgs) (any, error) {
		files, err := papp.InstallSessionProtocol(a.Project)
		if files == nil {
			files = []string{}
		}
		return map[string]any{"project": a.Project, "created": files}, err
	})
	reg["MirrorProject"] = projectAction("mirroring", func(a editArgs) (any, error) {
		return papp.MirrorProjectStatus(a.Project)
	})
	reg["UnmirrorProject"] = projectAction("unmirroring", func(a editArgs) (any, error) {
		return nil, papp.UnmirrorProject(a.Project)
	})
	reg["ShipLogsNow"] = func(_ json.RawMessage) (any, error) {
		if papp == nil {
			return nil, needApp("log shipping")
		}
		return papp.ShipLogsNowResult()
	}
	reg["GetLogShipStatus"] = func(_ json.RawMessage) (any, error) {
		if papp == nil {
			return nil, needApp("the log shipping status")
		}
		return papp.LogShipStatusOf(), nil
	}
	reg["SyncProjectMirrors"] = func(_ json.RawMessage) (any, error) {
		if papp == nil {
			return nil, needApp("mirror sync")
		}
		return papp.SyncProjectMirrorsResults()
	}
}
