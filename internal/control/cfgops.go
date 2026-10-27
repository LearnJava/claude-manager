package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"claude-manager/internal/config"
)

// Point edits of the config (MCP-08). A config value is edited in its TOML
// shape — the keys of config.example.toml, which is also what get_config
// returns — so an agent reads and writes one vocabulary. Every edit works on a
// copy, is validated by config.Validate (the code Load uses) and reports a
// was/now diff; nothing is saved here, the caller hands the result to the same
// UpdateConfig the GUI uses.

// FieldChange is one field of a diff.
type FieldChange struct {
	Was any `json:"was"`
	Now any `json:"now"`
}

// globalSettingsAllowed is the white list of update_global_settings: plain
// switches, limits and thresholds. Executable paths (claude_path, hermes_path)
// and the mirror folder are left out: they decide what the manager runs and
// where it copies files, and change only through update_config.
var globalSettingsAllowed = map[string]bool{
	"default_retry_delay": true, "rate_limit_pause": true, "log_retention_days": true,
	"theme": true, "language": true, "billing": true, "limit_window_tokens": true, "crash_recovery": true,
	"preflight_analysis": true, "preflight_model": true, "preflight_max_budget": true,
	"preflight_auto_approve_single": true,
	"permission_notify_after":       true, "permission_timeout": true, "permission_timeout_action": true,
	"permission_native_notification": true, "permission_sound": true,
	"daily_budget_alert": true, "weekly_budget_alert": true, "rate_limit_alert_threshold": true,
	"session_start_delay": true, "python_utf8": true, "command_guard": true,
	"env_card": true, "manager_card": true, "mirror_interval_min": true,
}

// tomlKeys lists the toml field names of struct type t.
func tomlKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("toml"), ",")
		if tag != "" && tag != "-" {
			keys[tag] = true
		}
	}
	return keys
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// toKeyed turns v into its TOML-shaped generic form.
func toKeyed(v any) (map[string]any, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	m := map[string]any{}
	if _, err := toml.Decode(buf.String(), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// fromKeyed decodes a TOML-shaped map into out (a pointer to a struct).
func fromKeyed(m map[string]any, out any) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return err
	}
	_, err := toml.Decode(buf.String(), out)
	return err
}

// normNumbers turns the float64 a JSON decoder produces for a whole number
// into int64, which is what TOML integers are.
func normNumbers(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
	case []any:
		for i := range x {
			x[i] = normNumbers(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = normNumbers(x[k])
		}
	}
	return v
}

// patchStruct applies a TOML-keyed patch to *cur. Keys outside allowed are
// refused, naming the allowed ones; null resets a field to its zero value.
func patchStruct[T any](cur *T, patch map[string]any, allowed map[string]bool, what string) (map[string]FieldChange, error) {
	for k := range patch {
		if !allowed[k] {
			return nil, fmt.Errorf("%s has no editable field %q: editable fields are %s", what, k, strings.Join(sortedKeys(allowed), ", "))
		}
	}
	before, err := toKeyed(cur)
	if err != nil {
		return nil, err
	}
	after := map[string]any{}
	for k, v := range before {
		after[k] = v
	}
	for k, v := range patch {
		if v == nil {
			delete(after, k)
			continue
		}
		after[k] = normNumbers(v)
	}
	var next T
	if err := fromKeyed(after, &next); err != nil {
		return nil, fmt.Errorf("%s: a value has the wrong type: %v", what, err)
	}
	// Diff on the re-encoded result so a reset field reports its zero value.
	nowKeyed, err := toKeyed(&next)
	if err != nil {
		return nil, err
	}
	changes := map[string]FieldChange{}
	for k := range patch {
		if !sameJSON(before[k], nowKeyed[k]) {
			changes[k] = FieldChange{Was: before[k], Now: nowKeyed[k]}
		}
	}
	*cur = next
	return changes, nil
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// ConfigEdit is the outcome of a point edit.
type ConfigEdit struct {
	Config  config.AppConfig // the edited copy, validated
	Changes map[string]FieldChange
}

func cloneConfig(c *config.AppConfig) config.AppConfig {
	n := *c
	n.Projects = make([]config.ProjectConfig, len(c.Projects))
	for i, p := range c.Projects {
		p.Sessions = append([]config.SessionConfig(nil), p.Sessions...)
		n.Projects[i] = p
	}
	n.Workers = append([]config.WorkerConfig(nil), c.Workers...)
	return n
}

func findProject(c *config.AppConfig, name string) int {
	for i, p := range c.Projects {
		if p.Name == name {
			return i
		}
	}
	return -1
}

func findSession(p *config.ProjectConfig, name string) int {
	for i, s := range p.Sessions {
		if s.Name == name {
			return i
		}
	}
	return -1
}

func cfgProjectNames(c *config.AppConfig) string {
	names := make([]string, 0, len(c.Projects))
	for _, p := range c.Projects {
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return "none configured"
	}
	return strings.Join(names, ", ")
}

func cfgSessionNames(p *config.ProjectConfig) string {
	names := make([]string, 0, len(p.Sessions))
	for _, s := range p.Sessions {
		names = append(names, s.Name)
	}
	if len(names) == 0 {
		return "none configured"
	}
	return strings.Join(names, ", ")
}

func finishEdit(c config.AppConfig, changes map[string]FieldChange) (*ConfigEdit, error) {
	if err := config.Validate(&c); err != nil {
		return nil, err
	}
	return &ConfigEdit{Config: c, Changes: changes}, nil
}

// pathKey makes project paths comparable: Windows paths ignore case.
func pathKey(p string) string { return strings.ToLower(filepath.Clean(p)) }

// CheckProjectDir reports a clear error unless path is an existing directory.
func CheckProjectDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is required: the project folder on the machine running Claude Manager")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path %q is not absolute: give the full folder path, e.g. D:\\Projects\\lumen", path)
	}
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("path %q does not exist: create the folder first or check the spelling", path)
	case err != nil:
		return fmt.Errorf("path %q cannot be read: %v", path, err)
	case !info.IsDir():
		return fmt.Errorf("path %q is a file, not a folder", path)
	}
	return nil
}

func addProject(c *config.AppConfig, name, path string) (*ConfigEdit, error) {
	if strings.TrimSpace(name) == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf(`project name %q is not valid: use a non-empty name without "/", e.g. lumen`, name)
	}
	if err := CheckProjectDir(path); err != nil {
		return nil, err
	}
	if findProject(c, name) >= 0 {
		return nil, fmt.Errorf("project %q already exists: pick another name or change it with update_project", name)
	}
	for _, p := range c.Projects {
		if pathKey(p.Path) == pathKey(path) {
			return nil, fmt.Errorf("folder %q is already project %q", path, p.Name)
		}
	}
	n := cloneConfig(c)
	n.Projects = append(n.Projects, config.ProjectConfig{Name: name, Path: path})
	return finishEdit(n, map[string]FieldChange{"project": {Was: nil, Now: map[string]any{"name": name, "path": path}}})
}

func removeProject(c *config.AppConfig, name string) (*ConfigEdit, error) {
	i := findProject(c, name)
	if i < 0 {
		return nil, fmt.Errorf("project %q not found: configured projects are %s", name, cfgProjectNames(c))
	}
	p := c.Projects[i]
	n := cloneConfig(c)
	n.Projects = append(n.Projects[:i], n.Projects[i+1:]...)
	return finishEdit(n, map[string]FieldChange{"project": {
		Was: map[string]any{"name": p.Name, "path": p.Path, "sessions": cfgSessionNames(&p)}, Now: nil}})
}

var projectEditable = func() map[string]bool {
	k := tomlKeys(reflect.TypeOf(config.ProjectConfig{}))
	delete(k, "name")
	delete(k, "path")
	delete(k, "session")
	return k
}()

var sessionEditable = func() map[string]bool {
	k := tomlKeys(reflect.TypeOf(config.SessionConfig{}))
	delete(k, "name")
	return k
}()

func updateProject(c *config.AppConfig, name string, patch map[string]any) (*ConfigEdit, error) {
	i := findProject(c, name)
	if i < 0 {
		return nil, fmt.Errorf("project %q not found: configured projects are %s", name, cfgProjectNames(c))
	}
	n := cloneConfig(c)
	changes, err := patchStruct(&n.Projects[i], patch, projectEditable, "project")
	if err != nil {
		return nil, err
	}
	return finishEdit(n, changes)
}

func addSession(c *config.AppConfig, project, name string, patch map[string]any) (*ConfigEdit, error) {
	i := findProject(c, project)
	if i < 0 {
		return nil, fmt.Errorf("project %q not found: configured projects are %s", project, cfgProjectNames(c))
	}
	if strings.TrimSpace(name) == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf(`session name %q is not valid: use a non-empty name without "/", e.g. P1`, name)
	}
	if findSession(&c.Projects[i], name) >= 0 {
		return nil, fmt.Errorf("session %s/%s already exists: change it with update_session", project, name)
	}
	n := cloneConfig(c)
	s := config.SessionConfig{Name: name}
	if _, err := patchStruct(&s, patch, sessionEditable, "session"); err != nil {
		return nil, err
	}
	n.Projects[i].Sessions = append(n.Projects[i].Sessions, s)
	return finishEdit(n, map[string]FieldChange{"session": {Was: nil, Now: map[string]any{"id": project + "/" + name}}})
}

func removeSession(c *config.AppConfig, project, name string) (*ConfigEdit, error) {
	i := findProject(c, project)
	if i < 0 {
		return nil, fmt.Errorf("project %q not found: configured projects are %s", project, cfgProjectNames(c))
	}
	j := findSession(&c.Projects[i], name)
	if j < 0 {
		return nil, fmt.Errorf("session %s/%s not found: sessions of %s are %s", project, name, project, cfgSessionNames(&c.Projects[i]))
	}
	n := cloneConfig(c)
	ss := n.Projects[i].Sessions
	n.Projects[i].Sessions = append(ss[:j:j], ss[j+1:]...)
	return finishEdit(n, map[string]FieldChange{"session": {Was: map[string]any{"id": project + "/" + name}, Now: nil}})
}

func updateSession(c *config.AppConfig, project, name string, patch map[string]any) (*ConfigEdit, error) {
	i := findProject(c, project)
	if i < 0 {
		return nil, fmt.Errorf("project %q not found: configured projects are %s", project, cfgProjectNames(c))
	}
	j := findSession(&c.Projects[i], name)
	if j < 0 {
		return nil, fmt.Errorf("session %s/%s not found: sessions of %s are %s", project, name, project, cfgSessionNames(&c.Projects[i]))
	}
	n := cloneConfig(c)
	changes, err := patchStruct(&n.Projects[i].Sessions[j], patch, sessionEditable, "session")
	if err != nil {
		return nil, err
	}
	return finishEdit(n, changes)
}

func updateGlobalSettings(c *config.AppConfig, patch map[string]any) (*ConfigEdit, error) {
	n := cloneConfig(c)
	changes, err := patchStruct(&n.Settings, patch, globalSettingsAllowed, "global settings")
	if err != nil {
		return nil, err
	}
	return finishEdit(n, changes)
}

// ── view and version ──────────────────────────────────────────────────────────

var cfgSecretKey = regexp.MustCompile(`(?i)(api_?key|secret|token|password|passwd)`)

// redact masks values under keys that look like secrets. The config holds
// none today (a worker names the environment variable of its key, key_env,
// never the key), so this is a guard for fields added later.
func redactConfig(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if s, isStr := e.(string); isStr && cfgSecretKey.MatchString(k) && s != "" {
				x[k] = "***"
			} else {
				x[k] = redactConfig(e)
			}
		}
	case []map[string]any:
		for i := range x {
			redactConfig(x[i])
		}
	case []any:
		for i := range x {
			x[i] = redactConfig(x[i])
		}
	}
	return v
}

// configView is the effective config in TOML shape, without secrets.
func configView(c *config.AppConfig) (map[string]any, error) {
	m, err := toKeyed(c)
	if err != nil {
		return nil, err
	}
	redactConfig(m)
	return m, nil
}

// ConfigVersion identifies the content of c: update_config only applies when
// the caller read the version it still sees.
func ConfigVersion(c *config.AppConfig) (string, error) {
	m, err := toKeyed(c)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(m) // map keys are sorted: stable
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8]), nil
}

// isActive reports whether a session status means a process is working.
func isActive(status string) bool {
	return status != "" && status != "idle" && status != "error"
}
