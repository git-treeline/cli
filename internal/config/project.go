package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const ProjectConfigFile = ".treeline.yml"

// identifierRe matches a valid SQL/redis-safe identifier: letter or underscore
// to start, then alphanumerics and underscores. The same rule applies to
// project names and database template names because they propagate into both.
var identifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// IsValidIdentifier reports whether s is a safe project / database identifier.
func IsValidIdentifier(s string) bool {
	return identifierRe.MatchString(s)
}

// SanitizeIdentifier converts an arbitrary string into a valid identifier by
// replacing runs of non-identifier characters with `_`, trimming leading and
// trailing underscores, and prefixing with `db_` when the result would start
// with a digit. Returns "app" for inputs that contain no usable characters.
func SanitizeIdentifier(s string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
		if valid {
			b.WriteRune(r)
			lastUnderscore = r == '_'
			continue
		}
		if !lastUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "app"
	}
	first := result[0]
	if (first >= '0' && first <= '9') {
		return "db_" + result
	}
	return result
}

var ProjectDefaults = map[string]any{
	"port_count": 1,
	"env_file": ".env.local",
	"database": map[string]any{
		"adapter":  "postgresql",
		"template": nil,
		"pattern":  "{template}_{worktree}",
	},
	"copy_files":   []any{},
	"env":          map[string]any{},
	"hooks":        map[string]any{},
	"commands":     map[string]any{},
	"editor":       map[string]any{},
	"merge_target": "",
}

type ProjectConfig struct {
	ProjectRoot string
	Data        map[string]any
}

func LoadProjectConfig(projectRoot string) *ProjectConfig {
	pc := &ProjectConfig{ProjectRoot: projectRoot}
	pc.Data = pc.load()
	pc.migrateDefaultBranch()
	pc.migrateCommands()
	pc.migrateEnvFile()
	pc.migrateEditor()
	pc.migratePortCount()
	return pc
}

func (pc *ProjectConfig) Project() string {
	if v, ok := pc.Data["project"].(string); ok && v != "" {
		return v
	}
	return SanitizeIdentifier(filepath.Base(pc.ProjectRoot))
}

// Validate checks that the loaded config has identifier-safe values for fields
// that propagate into databases, redis prefixes, and router keys. Returns nil
// when the config is safe to use, otherwise an error naming the offending
// field with a suggested replacement.
func (pc *ProjectConfig) Validate() error {
	if v, ok := pc.Data["project"].(string); ok && v != "" {
		if !IsValidIdentifier(v) {
			return fmt.Errorf("project name %q in %s contains invalid characters; must match [a-zA-Z_][a-zA-Z0-9_]* (run `gtl rename %s` to fix)",
				v, ProjectConfigFile, SanitizeIdentifier(v))
		}
	}
	if t := pc.DatabaseTemplate(); t != "" {
		adapter := pc.DatabaseAdapter()
		if (adapter == "postgresql" || adapter == "mysql") && !IsValidIdentifier(t) {
			return fmt.Errorf("database.template %q in %s is not a valid %s identifier; must match [a-zA-Z_][a-zA-Z0-9_]* (suggested: %s)",
				t, ProjectConfigFile, adapter, SanitizeIdentifier(t))
		}
	}
	return nil
}

func (pc *ProjectConfig) PortsNeeded() int {
	v := pc.Data["port_count"]
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 1
}

func (pc *ProjectConfig) DatabaseAdapter() string {
	if v, ok := Dig(pc.Data, "database", "adapter").(string); ok {
		return v
	}
	return "postgresql"
}

func (pc *ProjectConfig) DatabaseTemplate() string {
	v := Dig(pc.Data, "database", "template")
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (pc *ProjectConfig) DatabasePattern() string {
	if v, ok := Dig(pc.Data, "database", "pattern").(string); ok {
		return v
	}
	return "{template}_{worktree}"
}

// HasEnvFileConfig returns true if the config explicitly declares an env_file block.
func (pc *ProjectConfig) HasEnvFileConfig() bool {
	_, ok := pc.Data["env_file"]
	return ok
}

func (pc *ProjectConfig) EnvFileTarget() string {
	if s, ok := pc.Data["env_file"].(string); ok {
		return s
	}
	if v, ok := Dig(pc.Data, "env_file", "path").(string); ok {
		return v
	}
	if v, ok := Dig(pc.Data, "env_file", "target").(string); ok {
		return v
	}
	return ".env.local"
}

func (pc *ProjectConfig) EnvFileSource() string {
	if s, ok := pc.Data["env_file"].(string); ok {
		return s
	}
	if v, ok := Dig(pc.Data, "env_file", "seed_from").(string); ok {
		return v
	}
	if v, ok := Dig(pc.Data, "env_file", "source").(string); ok {
		return v
	}
	return ".env.local"
}

func (pc *ProjectConfig) CopyFiles() []string {
	raw, ok := pc.Data["copy_files"].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func (pc *ProjectConfig) EnvTemplate() map[string]string {
	raw, ok := pc.Data["env"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

// Hooks returns lifecycle hook commands keyed by hook name (pre_setup,
// post_setup, pre_release, post_release). Returns nil if no hooks are configured.
func (pc *ProjectConfig) Hooks() map[string][]string {
	raw, ok := pc.Data["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string][]string, len(raw))
	for name, v := range raw {
		if items, ok := v.([]any); ok {
			cmds := make([]string, 0, len(items))
			for _, item := range items {
				if s, ok := item.(string); ok {
					cmds = append(cmds, s)
				}
			}
			if len(cmds) > 0 {
				result[name] = cmds
			}
		}
	}
	return result
}

// StartHook defines a named hook activated via `gtl start --with <name>`
// or automatically on every fresh start when Auto is true.
// PreStart and PostStop each support a single string or an array of strings.
type StartHook struct {
	Auto     bool
	PreStart []string
	PostStop []string
}

// StartHooks returns named start-lifecycle hooks from the hooks: block.
// Only map-valued entries with pre_start/post_stop/auto keys are returned;
// array-valued entries (pre_setup, post_setup, etc.) are ignored.
func (pc *ProjectConfig) StartHooks() map[string]StartHook {
	raw, ok := pc.Data["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]StartHook)
	for name, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		h := StartHook{}
		if b, ok := m["auto"].(bool); ok {
			h.Auto = b
		}
		h.PreStart = parseStringOrArray(m["pre_start"])
		h.PostStop = parseStringOrArray(m["post_stop"])
		if len(h.PreStart) > 0 || len(h.PostStop) > 0 {
			result[name] = h
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// parseStringOrArray accepts a string or []any and returns a []string.
func parseStringOrArray(v any) []string {
	if s, ok := v.(string); ok && s != "" {
		return []string{s}
	}
	if items, ok := v.([]any); ok {
		out := make([]string, 0, len(items))
		for _, item := range items {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (pc *ProjectConfig) SetupCommands() []string {
	raw, ok := Dig(pc.Data, "commands", "setup").([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func (pc *ProjectConfig) Editor() map[string]string {
	raw, ok := pc.Data["editor"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	// Support old vscode_title as fallback for title
	if result["title"] == "" {
		if vt := result["vscode_title"]; vt != "" {
			result["title"] = vt
		}
	}
	return result
}

// EditorTitle returns the editor title template, or empty if not configured.
func (pc *ProjectConfig) EditorTitle() string {
	e := pc.Editor()
	if e == nil {
		return ""
	}
	return e["title"]
}

// EditorColor returns the editor color setting ("auto", a hex string, or empty).
func (pc *ProjectConfig) EditorColor() string {
	e := pc.Editor()
	if e == nil {
		return ""
	}
	return e["color"]
}

// EditorTheme returns the editor theme override, or empty if not configured.
func (pc *ProjectConfig) EditorTheme() string {
	e := pc.Editor()
	if e == nil {
		return ""
	}
	return e["theme"]
}

// Aliases returns static alias routes from the project config (e.g.
// {"redis-ui": 8081}). These are team-shared routes for sidecars or
// services that manage their own ports.
func (pc *ProjectConfig) Aliases() map[string]int {
	raw, ok := pc.Data["aliases"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]int, len(raw))
	for name, v := range raw {
		switch p := v.(type) {
		case int:
			result[name] = p
		case float64:
			result[name] = int(p)
		}
	}
	return result
}

func (pc *ProjectConfig) StartCommand() string {
	if v, ok := Dig(pc.Data, "commands", "start").(string); ok {
		return v
	}
	return ""
}

// MergeTarget returns the branch that prune --merged checks against.
func (pc *ProjectConfig) MergeTarget() string {
	if v, ok := pc.Data["merge_target"].(string); ok {
		return v
	}
	return ""
}

func (pc *ProjectConfig) Exists() bool {
	_, err := os.Stat(pc.configPath())
	return err == nil
}

// SetProject writes the project name to .treeline.yml. If a project: line
// exists, it's replaced in place. Otherwise the field is prepended.
func (pc *ProjectConfig) SetProject(name string) error {
	path := pc.configPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	content := string(raw)
	newLine := fmt.Sprintf("project: %s", name)

	lines := strings.Split(content, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "project:") {
			lines[i] = newLine
			found = true
			break
		}
	}
	if !found {
		lines = append([]string{newLine}, lines...)
	}

	pc.Data["project"] = name
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// migrateDefaultBranch rewrites default_branch → merge_target in the YAML
// file if the old key is present. Runs once per load, idempotent.
func (pc *ProjectConfig) migrateDefaultBranch() {
	old, ok := pc.Data["default_branch"].(string)
	if !ok || old == "" {
		return
	}

	if mt, _ := pc.Data["merge_target"].(string); mt == "" {
		pc.Data["merge_target"] = old
	}
	delete(pc.Data, "default_branch")

	path := pc.configPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := strings.Replace(string(raw), "default_branch:", "merge_target:", 1)
	_ = os.WriteFile(path, []byte(content), 0o644)
}

// migrateCommands rewrites setup_commands/start_command → commands.setup/start
// in the YAML file if old keys are present. Idempotent.
func (pc *ProjectConfig) migrateCommands() {
	setupCmds, hasSetup := pc.Data["setup_commands"]
	startCmd, hasStart := pc.Data["start_command"]
	if !hasSetup && !hasStart {
		return
	}

	cmds, _ := pc.Data["commands"].(map[string]any)
	if cmds == nil {
		cmds = map[string]any{}
	}

	if hasSetup {
		if _, exists := cmds["setup"]; !exists {
			cmds["setup"] = setupCmds
		}
		delete(pc.Data, "setup_commands")
	}
	if hasStart {
		if s, ok := startCmd.(string); ok && s != "" {
			if _, exists := cmds["start"]; !exists {
				cmds["start"] = s
			}
		}
		delete(pc.Data, "start_command")
	}
	pc.Data["commands"] = cmds

	path := pc.configPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(raw)

	if hasSetup {
		content = rewriteSetupCommands(content)
	}
	if hasStart {
		if s, ok := startCmd.(string); ok && s != "" {
			content = strings.Replace(content, "start_command: "+s, "", 1)
			content = strings.ReplaceAll(content, "\n\n\n", "\n\n")
		}
	}
	_ = os.WriteFile(path, []byte(content), 0o644)
}

// rewriteSetupCommands converts the flat setup_commands key into a commands.setup block.
func rewriteSetupCommands(content string) string {
	lines := strings.Split(content, "\n")
	var out []string
	inSetupCmds := false
	var setupItems []string

	for _, line := range lines {
		if strings.HasPrefix(line, "setup_commands:") {
			inSetupCmds = true
			continue
		}
		if inSetupCmds {
			if strings.HasPrefix(line, "  - ") {
				setupItems = append(setupItems, line)
				continue
			}
			inSetupCmds = false
		}
		out = append(out, line)
	}

	if len(setupItems) > 0 {
		out = appendCommandsBlock(out, "setup", setupItems)
	}
	return strings.Join(out, "\n")
}

func appendCommandsBlock(lines []string, key string, items []string) []string {
	for i, line := range lines {
		if line == "commands:" {
			insert := []string{"  " + key + ":"}
			for _, item := range items {
				insert = append(insert, "  "+item)
			}
			result := make([]string, 0, len(lines)+len(insert))
			result = append(result, lines[:i+1]...)
			result = append(result, insert...)
			result = append(result, lines[i+1:]...)
			return result
		}
	}
	result := append(lines, "", "commands:", "  "+key+":")
	for _, item := range items {
		result = append(result, "  "+item)
	}
	return result
}

// migrateEnvFile rewrites env_file from the old target/source map form to the
// new string shorthand or path/seed_from map. Idempotent.
func (pc *ProjectConfig) migrateEnvFile() {
	m, ok := pc.Data["env_file"].(map[string]any)
	if !ok {
		return
	}
	target, _ := m["target"].(string)
	source, _ := m["source"].(string)
	if target == "" || m["path"] != nil {
		return
	}

	path := pc.configPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(raw)

	var newContent string
	if target == source || source == "" {
		newContent = rewriteEnvFileToSimple(content, target)
	} else {
		newContent = rewriteEnvFileToExtended(content, target, source)
	}

	if newContent == content {
		return
	}

	if target == source || source == "" {
		pc.Data["env_file"] = target
	} else {
		pc.Data["env_file"] = map[string]any{"path": target, "seed_from": source}
	}
	_ = os.WriteFile(path, []byte(newContent), 0o644)
}

// migrateEditor rewrites editor.vscode_title → editor.title in the YAML file
// and in-memory data. Idempotent.
func (pc *ProjectConfig) migrateEditor() {
	editorMap, ok := pc.Data["editor"].(map[string]any)
	if !ok {
		return
	}
	vt, hasOld := editorMap["vscode_title"].(string)
	_, hasNew := editorMap["title"].(string)
	if !hasOld || hasNew {
		return
	}

	editorMap["title"] = vt
	delete(editorMap, "vscode_title")

	path := pc.configPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := strings.Replace(string(raw), "vscode_title:", "title:", 1)
	_ = os.WriteFile(path, []byte(content), 0o644)
}

func (pc *ProjectConfig) migratePortCount() {
	_, hasOld := pc.Data["ports_needed"]
	if !hasOld {
		return
	}

	path := pc.configPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(raw)

	if strings.Contains(content, "port_count:") {
		delete(pc.Data, "ports_needed")
		return
	}

	pc.Data["port_count"] = pc.Data["ports_needed"]
	delete(pc.Data, "ports_needed")

	_, _ = fmt.Fprintf(os.Stderr, "Warning: ports_needed is deprecated, renamed to port_count in %s\n", path)
	content = strings.Replace(content, "ports_needed:", "port_count:", 1)
	_ = os.WriteFile(path, []byte(content), 0o644)
}

// rewriteEnvFileToSimple collapses the block-style env_file to a single line.
func rewriteEnvFileToSimple(content, target string) string {
	return rewriteEnvFileBlock(content, fmt.Sprintf("env_file: %s", target))
}

// rewriteEnvFileToExtended renames target/source keys to path/seed_from.
func rewriteEnvFileToExtended(content, target, seedFrom string) string {
	replacement := fmt.Sprintf("env_file:\n  path: %s\n  seed_from: %s", target, seedFrom)
	return rewriteEnvFileBlock(content, replacement)
}

func rewriteEnvFileBlock(content, replacement string) string {
	lines := strings.Split(content, "\n")
	var out []string
	skip := false

	for _, line := range lines {
		if strings.TrimSpace(line) == "env_file:" {
			out = append(out, replacement)
			skip = true
			continue
		}
		if skip {
			if line == "" || (len(line) > 0 && line[0] != ' ' && line[0] != '\t') {
				skip = false
				out = append(out, line)
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (pc *ProjectConfig) configPath() string {
	return filepath.Join(pc.ProjectRoot, ProjectConfigFile)
}

var projectKnownKeys = map[string]bool{
	"project": true, "port_count": true, "env_file": true, "database": true,
	"copy_files": true, "env": true, "hooks": true, "commands": true,
	"editor": true, "merge_target": true, "aliases": true,
	// Legacy keys accepted during migration
	"default_branch": true, "setup_commands": true, "start_command": true,
	"ports_needed": true,
}

func (pc *ProjectConfig) load() map[string]any {
	raw, err := os.ReadFile(pc.configPath())
	if err != nil {
		return copyMap(ProjectDefaults)
	}

	var yamlData map[string]any
	if err := yaml.Unmarshal(raw, &yamlData); err != nil || yamlData == nil {
		return copyMap(ProjectDefaults)
	}

	WarnUnknownKeys(yamlData, projectKnownKeys, ProjectConfigFile)

	return DeepMerge(ProjectDefaults, yamlData)
}
