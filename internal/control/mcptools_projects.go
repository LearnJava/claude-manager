package control

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tools of the projects-and-configuration area (MCP-08): read the setup, edit
// the config point by point, and act on a project's folder. Config edits take
// and return values in the TOML shape of config.example.toml.

type getConfigIn struct {
	Section string `json:"section,omitempty" jsonschema:"Part of the config to return: settings, optimization, project or worker; omit for all of it"`
}

type addProjectIn struct {
	Name   string `json:"name" jsonschema:"New project name, unique, no slash, e.g. lumen"`
	Path   string `json:"path" jsonschema:"Absolute path of the project folder on the machine running Claude Manager, e.g. D:\\Projects\\lumen; it must exist"`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"True to validate and show the diff without saving"`
}

type removeProjectIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	DryRun  bool   `json:"dry_run,omitempty" jsonschema:"True to validate and show the diff without saving"`
}

type updateProjectIn struct {
	Project string         `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Fields  map[string]any `json:"fields" jsonschema:"Fields to change by their config.toml names, e.g. {\"mixed_programming\": true, \"gates\": [\"go test ./...\"]}; null resets a field. name, path and session are not editable here"`
	DryRun  bool           `json:"dry_run,omitempty" jsonschema:"True to validate and show the diff without saving"`
}

type addSessionIn struct {
	Project string         `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Name    string         `json:"name" jsonschema:"New session name, unique in the project, no slash, e.g. P1"`
	Fields  map[string]any `json:"fields,omitempty" jsonschema:"Optional initial fields by their config.toml names, e.g. {\"model\": \"sonnet\", \"prompt\": \"Work through the queue\"}"`
	DryRun  bool           `json:"dry_run,omitempty" jsonschema:"True to validate and show the diff without saving"`
}

type removeSessionIn struct {
	sessionRef
	DryRun bool `json:"dry_run,omitempty" jsonschema:"True to validate and show the diff without saving"`
}

type updateSessionIn struct {
	sessionRef
	Fields map[string]any `json:"fields" jsonschema:"Fields to change by their config.toml names, e.g. {\"model\": \"opus\", \"max_budget_usd\": 5}; null resets a field. name is not editable"`
	DryRun bool           `json:"dry_run,omitempty" jsonschema:"True to validate and show the diff without saving"`
}

type updateGlobalIn struct {
	Fields map[string]any `json:"fields" jsonschema:"Settings to change by their config.toml names, e.g. {\"daily_budget_alert\": 20, \"language\": \"ru\"}; only the editable settings listed in the error of an unknown name are accepted"`
	DryRun bool           `json:"dry_run,omitempty" jsonschema:"True to validate and show the diff without saving"`
}

type updateConfigIn struct {
	Config  map[string]any `json:"config" jsonschema:"The whole config in the shape get_config returns (settings, optimization, project, worker), edited"`
	Version string         `json:"version" jsonschema:"The version returned by the get_config call the edit is based on"`
}

type pathOptIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
}

type projectsOut struct {
	Projects []any `json:"projects" jsonschema:"Projects with name, path, session names, mirror and mixed_programming"`
	pageOut
}

type configOut struct {
	Version string         `json:"version" jsonschema:"Version of the config, to pass to update_config"`
	Config  map[string]any `json:"config" jsonschema:"The effective config in config.toml shape, without secrets"`
}

type environmentOut struct {
	Report any `json:"report" jsonschema:"The programs found (Claude CLI, Git, Python, build tools) with versions, and for each missing one the command that installs it"`
}

type setupOut struct {
	Project         string `json:"project" jsonschema:"The project asked about"`
	Path            string `json:"path" jsonschema:"The project folder"`
	ClaudeMd        bool   `json:"claude_md" jsonschema:"True when the folder has a CLAUDE.md"`
	SessionProtocol bool   `json:"session_protocol" jsonschema:"True when the folder carries the developer-session protocol"`
	Mirror          any    `json:"mirror" jsonschema:"Mirror state: enabled, dir, last_sync, files, error"`
}

type editOut struct {
	OK      bool           `json:"ok" jsonschema:"True when the request was valid"`
	DryRun  bool           `json:"dry_run" jsonschema:"True when nothing was saved because dry_run was set"`
	Changed bool           `json:"changed" jsonschema:"True when the config was changed and saved"`
	Changes map[string]any `json:"changes" jsonschema:"Per field, was and now; empty when the request changes nothing"`
	Version string         `json:"version" jsonschema:"Config version after the call"`
}

type protocolOut struct {
	OK      bool     `json:"ok" jsonschema:"True when the action finished"`
	Project string   `json:"project" jsonschema:"The project acted on"`
	Created []string `json:"created" jsonschema:"Files created; files that already existed are left untouched"`
}

type mirrorOut struct {
	OK     bool `json:"ok" jsonschema:"True when the action finished"`
	Mirror any  `json:"mirror" jsonschema:"Mirror state: enabled, dir, last_sync, files, error; or the per-project sync results"`
}

func editResult(raw any) editOut {
	m, _ := raw.(map[string]any)
	out := editOut{OK: true, Changes: map[string]any{}}
	out.DryRun, _ = m["dry_run"].(bool)
	out.Changed, _ = m["changed"].(bool)
	if c, ok := m["changes"].(map[string]any); ok {
		out.Changes = c
	}
	out.Version, _ = m["version"].(string)
	return out
}

func editSummary(what string) func(editOut) string {
	return func(out editOut) string {
		fields := make([]string, 0, len(out.Changes))
		for k := range out.Changes {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		list := strings.Join(fields, ", ")
		switch {
		case len(fields) == 0:
			return what + ": nothing to change."
		case out.DryRun:
			return fmt.Sprintf("%s: dry run, would change %s.", what, list)
		}
		return fmt.Sprintf("%s: changed %s.", what, list)
	}
}

func needFields(fields map[string]any) error {
	if len(fields) == 0 {
		return fmt.Errorf(`fields is required, e.g. {"model":"opus"}`)
	}
	return nil
}

func projectsTools() []Tool {
	ro := ToolAnnotations{ReadOnly: true}
	edit := ToolAnnotations{Idempotent: true}
	gone := ToolAnnotations{Destructive: true, Idempotent: true}
	opWorld := ToolAnnotations{Idempotent: true, OpenWorld: true}
	const dryNote = " dry_run:true returns the diff and saves nothing."
	const changeFx = "rewrites the saved configuration and the project's .claude-manager folder; the GUI shows it after its next reload."

	return []Tool{
		define(spec[pageIn, projectsOut]{
			Name: "get_projects", Title: "List projects",
			Doc: Doc{
				What:    "List the configured projects with their folder, session names and mirror and mixed-programming flags.",
				UseWhen: "you need a project name or folder before calling another tool.",
				NotFor:  "a project's full settings (use get_config) or session states (use get_sessions).",
				Returns: "projects, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetProjects"},
			Translate: func(in pageIn) (string, any, error) {
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetProjects", map[string]string{}), nil
			},
			Result: func(in pageIn, raw any) projectsOut {
				page, p := listPage(in, raw)
				return projectsOut{Projects: page, pageOut: p}
			},
			Summary: func(_ pageIn, out projectsOut) string {
				return plural(len(out.Projects), "project", "projects") + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[getConfigIn, configOut]{
			Name: "get_config", Title: "Get effective config",
			Doc: Doc{
				What:    "Return the effective application config (global settings, optimization, projects with sessions, workers) in config.toml shape, without secrets, with its version.",
				UseWhen: "you need a setting's value, or before update_config, which needs the version.",
				NotFor:  "listing projects (use get_projects) or changing one setting (use update_session, update_project or update_global_settings).",
				Returns: "version and config; pass section to get one part, the full config can be large.",
				Effects: "none, read only.",
				Example: `{"section":"settings"}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				props["section"].Enum = []any{"settings", "optimization", "project", "worker"}
			},
			Level: LevelRead, Annotations: ro,
			Translate: func(getConfigIn) (string, any, error) {
				return "/rpc", rpcCallBody("GetConfigView", map[string]string{}), nil
			},
			Result: func(in getConfigIn, raw any) configOut {
				m, _ := raw.(map[string]any)
				out := configOut{Config: map[string]any{}}
				out.Version, _ = m["version"].(string)
				cfg, _ := m["config"].(map[string]any)
				for k, v := range cfg {
					if in.Section == "" || in.Section == k {
						out.Config[k] = v
					}
				}
				return out
			},
			Summary: func(in getConfigIn, out configOut) string {
				if in.Section != "" {
					return fmt.Sprintf("Config section %s, version %s.", in.Section, out.Version)
				}
				return fmt.Sprintf("Config version %s.", out.Version)
			},
		}),
		define(spec[noIn, environmentOut]{
			Name: "check_environment", Title: "Check the environment",
			Doc: Doc{
				What:    "Find the programs sessions rely on (Claude CLI, Git, Git Bash, Python, build tools) and the install command for each missing one.",
				UseWhen: "a session fails to start or a tool is missing.",
				NotFor:  "a session's own state (use get_session).",
				Returns: "the report of found and missing programs.",
				Effects: "runs each found program's version command, one to two seconds; changes nothing.",
				Example: `{}`,
			},
			Level: LevelRead, Annotations: ToolAnnotations{ReadOnly: true, OpenWorld: true}, Covers: []string{"CheckEnvironment"},
			Translate: func(noIn) (string, any, error) {
				return "/rpc", rpcCallBody("CheckEnvironment", map[string]string{}), nil
			},
			Result:  func(_ noIn, raw any) environmentOut { return environmentOut{Report: raw} },
			Summary: func(noIn, environmentOut) string { return "Environment checked." },
		}),
		define(spec[pathOptIn, setupOut]{
			Name: "get_project_setup", Title: "Get project setup",
			Doc: Doc{
				What:    "Report whether a project's folder has a CLAUDE.md and the developer-session protocol, and its mirror state.",
				UseWhen: "before generate_claude_md, install_session_protocol or mirror_project, or to audit a project.",
				NotFor:  "the project's config (use get_config).",
				Returns: "claude_md, session_protocol and mirror.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"HasClaudeMd", "HasSessionProtocol", "GetProjectMirror"},
			Translate: func(in pathOptIn) (string, any, error) {
				return "/rpc", rpcCallBody("GetProjectSetup", map[string]string{"project": in.Project}), nil
			},
			Result: func(_ pathOptIn, raw any) setupOut {
				m, _ := raw.(map[string]any)
				out := setupOut{Mirror: m["mirror"]}
				out.Project, _ = m["project"].(string)
				out.Path, _ = m["path"].(string)
				out.ClaudeMd, _ = m["claude_md"].(bool)
				out.SessionProtocol, _ = m["session_protocol"].(bool)
				return out
			},
			Summary: func(_ pathOptIn, out setupOut) string {
				return fmt.Sprintf("%s: CLAUDE.md %t, protocol %t.", out.Project, out.ClaudeMd, out.SessionProtocol)
			},
		}),

		define(spec[addProjectIn, editOut]{
			Name: "add_project", Title: "Add project",
			Doc: Doc{
				What:    "Register a project folder under a new name." + dryNote,
				UseWhen: "a folder should be managed; add its sessions with add_session afterwards.",
				NotFor:  "creating the folder (it must already exist) or adding a session.",
				Returns: "the diff, whether it was saved and the new config version.",
				Effects: changeFx + " A folder that already carries a .claude-manager config brings its sessions along.",
				Example: `{"name":"lumen","path":"D:\\Projects\\lumen"}`,
			},
			Level: LevelAdmin, Annotations: edit, Covers: nil,
			Translate: func(in addProjectIn) (string, any, error) {
				if in.Name == "" {
					return "", nil, fmt.Errorf(`name is required, e.g. "lumen"`)
				}
				if err := CheckProjectDir(in.Path); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("AddProject", map[string]any{"name": in.Name, "path": in.Path, "dry_run": in.DryRun}), nil
			},
			Result:  func(_ addProjectIn, raw any) editOut { return editResult(raw) },
			Summary: func(in addProjectIn, out editOut) string { return editSummary("Project " + in.Name)(out) },
		}),
		define(spec[updateProjectIn, editOut]{
			Name: "update_project", Title: "Update project settings",
			Doc: Doc{
				What:    "Change fields of a project: default permission mode, mixed programming, gates, journal, skills and mirror flags." + dryNote,
				UseWhen: "a project-level option must change.",
				NotFor:  "session options (use update_session), the project's name or folder (remove and add it again) or mirroring now (use mirror_project).",
				Returns: "the diff (was and now per field), whether it was saved and the new config version.",
				Effects: changeFx,
				Example: `{"project":"lumen","fields":{"gates":["go test ./..."]},"dry_run":true}`,
			},
			Level: LevelAdmin, Annotations: edit,
			Translate: func(in updateProjectIn) (string, any, error) {
				if in.Project == "" {
					return "", nil, fmt.Errorf(`project is required, e.g. "lumen"`)
				}
				if err := needFields(in.Fields); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("UpdateProject", map[string]any{"project": in.Project, "fields": in.Fields, "dry_run": in.DryRun}), nil
			},
			Result:  func(_ updateProjectIn, raw any) editOut { return editResult(raw) },
			Summary: func(in updateProjectIn, out editOut) string { return editSummary("Project " + in.Project)(out) },
		}),
		define(spec[removeProjectIn, editOut]{
			Name: "remove_project", Title: "Remove project",
			Doc: Doc{
				What:    "Remove a project from the manager's registry." + dryNote,
				UseWhen: "a project should no longer be managed.",
				NotFor:  "deleting files: the folder and its .claude-manager config stay, and adding the folder again restores its sessions.",
				Returns: "the diff, whether it was saved and the new config version.",
				Effects: changeFx + " Refused while any session of the project is running.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelAdmin, Annotations: gone,
			Translate: func(in removeProjectIn) (string, any, error) {
				if in.Project == "" {
					return "", nil, fmt.Errorf(`project is required, e.g. "lumen"`)
				}
				return "/rpc", rpcCallBody("RemoveProject", map[string]any{"name": in.Project, "dry_run": in.DryRun}), nil
			},
			Result:  func(_ removeProjectIn, raw any) editOut { return editResult(raw) },
			Summary: func(in removeProjectIn, out editOut) string { return editSummary("Project " + in.Project)(out) },
		}),
		define(spec[addSessionIn, editOut]{
			Name: "add_session", Title: "Add session",
			Doc: Doc{
				What:    "Add a session to a project, optionally with initial fields." + dryNote,
				UseWhen: "a project needs another long-lived session.",
				NotFor:  "an ad-hoc chat (use start_ad_hoc_chat) or changing an existing session (use update_session).",
				Returns: "the diff, whether it was saved and the new config version.",
				Effects: changeFx + " The session is not started: use start_session.",
				Example: `{"project":"lumen","name":"P1","fields":{"model":"sonnet"}}`,
			},
			Level: LevelAdmin, Annotations: edit,
			Translate: func(in addSessionIn) (string, any, error) {
				if in.Project == "" || in.Name == "" {
					return "", nil, fmt.Errorf(`project and name are required, e.g. {"project":"lumen","name":"P1"}`)
				}
				return "/rpc", rpcCallBody("AddSession", map[string]any{"project": in.Project, "session": in.Name, "fields": in.Fields, "dry_run": in.DryRun}), nil
			},
			Result: func(_ addSessionIn, raw any) editOut { return editResult(raw) },
			Summary: func(in addSessionIn, out editOut) string {
				return editSummary("Session " + in.Project + "/" + in.Name)(out)
			},
		}),
		define(spec[updateSessionIn, editOut]{
			Name: "update_session", Title: "Update session settings",
			Doc: Doc{
				What:    "Change fields of a session: model, prompt, budget, permission mode, hooks and the rest of its config." + dryNote,
				UseWhen: "a session's saved options must change; a running session picks them up on its next start.",
				NotFor:  "switching the model of a running session at once (use set_session_model) or the project's options (use update_project).",
				Returns: "the diff (was and now per field), whether it was saved and the new config version.",
				Effects: changeFx,
				Example: `{"session_id":"lumen/P1","fields":{"model":"opus"}}`,
			},
			Level: LevelAdmin, Annotations: edit,
			Translate: func(in updateSessionIn) (string, any, error) {
				project, name, err := in.parts()
				if err != nil {
					return "", nil, err
				}
				if err := needFields(in.Fields); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("UpdateSession", map[string]any{"project": project, "session": name, "fields": in.Fields, "dry_run": in.DryRun}), nil
			},
			Result: func(_ updateSessionIn, raw any) editOut { return editResult(raw) },
			Summary: func(in updateSessionIn, out editOut) string {
				id, _ := in.id()
				return editSummary("Session " + id)(out)
			},
		}),
		define(spec[removeSessionIn, editOut]{
			Name: "remove_session", Title: "Remove session",
			Doc: Doc{
				What:    "Remove a session from its project's config." + dryNote,
				UseWhen: "a session is no longer needed.",
				NotFor:  "stopping a running session (use stop_session) or clearing its saved state (use clear_session_state).",
				Returns: "the diff, whether it was saved and the new config version.",
				Effects: changeFx + " Refused while the session is running.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Level: LevelAdmin, Annotations: gone,
			Translate: func(in removeSessionIn) (string, any, error) {
				project, name, err := in.parts()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("RemoveSession", map[string]any{"project": project, "session": name, "dry_run": in.DryRun}), nil
			},
			Result: func(_ removeSessionIn, raw any) editOut { return editResult(raw) },
			Summary: func(in removeSessionIn, out editOut) string {
				id, _ := in.id()
				return editSummary("Session " + id)(out)
			},
		}),
		define(spec[updateGlobalIn, editOut]{
			Name: "update_global_settings", Title: "Update global settings",
			Doc: Doc{
				What:    "Change application-wide settings: budgets and alerts, permission handling, retry and log retention, language, cards." + dryNote,
				UseWhen: "an application-wide option must change; only the fields you give change.",
				NotFor:  "executable paths or the whole config (use update_config) and per-project or per-session options.",
				Returns: "the diff (was and now per field), whether it was saved and the new config version.",
				Effects: changeFx,
				Example: `{"fields":{"daily_budget_alert":20}}`,
			},
			Level: LevelAdmin, Annotations: edit,
			Translate: func(in updateGlobalIn) (string, any, error) {
				if err := needFields(in.Fields); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("UpdateGlobalSettings", map[string]any{"fields": in.Fields, "dry_run": in.DryRun}), nil
			},
			Result:  func(_ updateGlobalIn, raw any) editOut { return editResult(raw) },
			Summary: func(_ updateGlobalIn, out editOut) string { return editSummary("Global settings")(out) },
		}),
		define(spec[updateConfigIn, configVersionOut]{
			Name: "update_config", Title: "Replace the whole config",
			Doc: Doc{
				What:    "Replace the whole application config with an edited copy of what get_config returned.",
				UseWhen: "an edit needs fields the point tools do not reach, such as executable paths or workers.",
				NotFor:  "ordinary edits (use update_project, update_session or update_global_settings).",
				Returns: "the new config version; refused when the config changed since the version you pass.",
				Effects: changeFx + " Whatever you leave out is removed.",
				Example: `{"config":{"settings":{}},"version":"3f9a1c2b4d5e6f70"}`,
			},
			Level: LevelAdmin, Annotations: ToolAnnotations{Destructive: true, Idempotent: true},
			Translate: func(in updateConfigIn) (string, any, error) {
				if len(in.Config) == 0 {
					return "", nil, fmt.Errorf("config is required: pass the object returned by get_config, edited")
				}
				if in.Version == "" {
					return "", nil, fmt.Errorf("version is required: pass the version returned by get_config")
				}
				return "/rpc", rpcCallBody("ReplaceConfig", map[string]any{"config": in.Config, "version": in.Version}), nil
			},
			Result: func(_ updateConfigIn, raw any) configVersionOut {
				m, _ := raw.(map[string]any)
				v, _ := m["version"].(string)
				return configVersionOut{OK: true, Version: v}
			},
			Summary: func(_ updateConfigIn, out configVersionOut) string {
				return "Config replaced, version " + out.Version + "."
			},
		}),

		define(spec[pathOptIn, projectAck]{
			Name: "init_git_repo", Title: "Initialise a git repository",
			Doc: Doc{
				What:    "Run git init in the project folder if it is not a repository yet, and make an initial commit if HEAD cannot be resolved.",
				UseWhen: "a session with use_worktree fails because the folder is not a usable git repository.",
				NotFor:  "projects that already are repositories (it changes nothing there).",
				Returns: "ok and the project.",
				Effects: "writes .git and possibly one commit into the user's project folder; restart the session afterwards.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelAdmin, Annotations: opWorld, Covers: []string{"InitGitRepo"},
			Translate: func(in pathOptIn) (string, any, error) {
				return "/rpc", rpcCallBody("InitGitRepo", map[string]string{"project": in.Project}), nil
			},
			Result:  func(in pathOptIn, _ any) projectAck { return projectAck{OK: true, Project: in.Project} },
			Summary: func(in pathOptIn, _ projectAck) string { return "Git repository ready in " + in.Project + "." },
		}),
		define(spec[pathOptIn, projectAck]{
			Name: "generate_claude_md", Title: "Generate CLAUDE.md",
			Doc: Doc{
				What:    "Create or re-point the Init session of a project and start it, so Claude writes (or refines) the project's CLAUDE.md.",
				UseWhen: "get_project_setup shows claude_md false, or the file is stale.",
				NotFor:  "writing CLAUDE.md yourself.",
				Returns: "ok and the project; follow the Init session with get_session_logs.",
				Effects: "adds or edits the Init session in the config, starts it and spends tokens; it writes CLAUDE.md into the user's project folder.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelAdmin, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"GenerateClaudeMdSession"},
			Translate: func(in pathOptIn) (string, any, error) {
				return "/rpc", rpcCallBody("GenerateClaudeMdSession", map[string]string{"project": in.Project}), nil
			},
			Result:  func(in pathOptIn, _ any) projectAck { return projectAck{OK: true, Project: in.Project} },
			Summary: func(in pathOptIn, _ projectAck) string { return "Init session started for " + in.Project + "." },
		}),
		define(spec[pathOptIn, protocolOut]{
			Name: "install_session_protocol", Title: "Install the session protocol",
			Doc: Doc{
				What:    "Write the developer-session protocol (docs/git-workflow.md, scripts/worktree-pool.sh, the cm-task skills) into a project and gitignore its worktree pool.",
				UseWhen: "get_project_setup shows session_protocol false for a project that runs queue-driven sessions.",
				NotFor:  "projects that already have their own protocol: existing files are never overwritten.",
				Returns: "the files created; empty when everything was already there.",
				Effects: "writes files into the user's project folder and edits its .gitignore.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelAdmin, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"InstallSessionProtocol"},
			Translate: func(in pathOptIn) (string, any, error) {
				return "/rpc", rpcCallBody("InstallSessionProtocol", map[string]string{"project": in.Project}), nil
			},
			Result: func(in pathOptIn, raw any) protocolOut {
				m, _ := raw.(map[string]any)
				out := protocolOut{OK: true, Project: in.Project, Created: []string{}}
				for _, f := range list(m["created"]) {
					if s, ok := f.(string); ok {
						out.Created = append(out.Created, s)
					}
				}
				return out
			},
			Summary: func(in pathOptIn, out protocolOut) string {
				return fmt.Sprintf("%s: %s created.", in.Project, plural(len(out.Created), "file", "files"))
			},
		}),
		define(spec[pathOptIn, mirrorOut]{
			Name: "mirror_project", Title: "Mirror project",
			Doc: Doc{
				What:    "Turn on \"Mirror into Claude Manager\" for a project and copy its settings and saved run logs now.",
				UseWhen: "the project's setup should be readable from the manager's own checkout.",
				NotFor:  "writing into the project (the copy is read-only).",
				Returns: "the mirror state: dir, last_sync, files.",
				Effects: "sets the project's mirror flag in the config and writes a copy into the manager's mirror folder; the project is not changed.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelAdmin, Annotations: edit, Covers: []string{"MirrorProject"},
			Translate: func(in pathOptIn) (string, any, error) {
				return "/rpc", rpcCallBody("MirrorProject", map[string]string{"project": in.Project}), nil
			},
			Result:  func(_ pathOptIn, raw any) mirrorOut { return mirrorOut{OK: true, Mirror: raw} },
			Summary: func(in pathOptIn, _ mirrorOut) string { return "Project " + in.Project + " mirrored." },
		}),
		define(spec[pathOptIn, projectAck]{
			Name: "unmirror_project", Title: "Stop mirroring a project",
			Doc: Doc{
				What:    "Stop re-syncing a project's mirror.",
				UseWhen: "the copy is no longer wanted up to date.",
				NotFor:  "deleting the copy: it and its history stay in the mirror folder.",
				Returns: "ok and the project.",
				Effects: "clears the project's mirror flag in the config.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelAdmin, Annotations: edit, Covers: []string{"UnmirrorProject"},
			Translate: func(in pathOptIn) (string, any, error) {
				return "/rpc", rpcCallBody("UnmirrorProject", map[string]string{"project": in.Project}), nil
			},
			Result:  func(in pathOptIn, _ any) projectAck { return projectAck{OK: true, Project: in.Project} },
			Summary: func(in pathOptIn, _ projectAck) string { return "Project " + in.Project + " no longer mirrored." },
		}),
		define(spec[noIn, mirrorOut]{
			Name: "sync_project_mirrors", Title: "Sync project mirrors",
			Doc: Doc{
				What:    "Re-sync every mirrored project now.",
				UseWhen: "a mirror should be current before you read it.",
				NotFor:  "turning mirroring on or off (use mirror_project or unmirror_project).",
				Returns: "one result per mirrored project: files, changed, error.",
				Effects: "writes into the manager's mirror folder; projects are not changed.",
				Example: `{}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"SyncProjectMirrors"},
			Translate: func(noIn) (string, any, error) {
				return "/rpc", rpcCallBody("SyncProjectMirrors", map[string]string{}), nil
			},
			Result:  func(_ noIn, raw any) mirrorOut { return mirrorOut{OK: true, Mirror: raw} },
			Summary: func(noIn, mirrorOut) string { return "Mirrors synced." },
		}),
	}
}

type configVersionOut struct {
	OK      bool   `json:"ok" jsonschema:"True when the config was replaced"`
	Version string `json:"version" jsonschema:"Config version after the call"`
}
