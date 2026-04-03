package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const ProjectConfigFile = ".treeline.yml"

var ProjectDefaults = map[string]any{
	"ports_needed": 1,
	"env_file": map[string]any{
		"target": ".env.local",
		"source": ".env.local",
	},
	"database": map[string]any{
		"adapter":  "postgresql",
		"template": nil,
		"pattern":  "{template}_{worktree}",
	},
	"copy_files":     []any{},
	"env":            map[string]any{},
	"setup_commands":  []any{},
	"editor":          map[string]any{},
	"start_command":   "",
	"merge_target":    "",
}

type ProjectConfig struct {
	ProjectRoot string
	Data        map[string]any
}

func LoadProjectConfig(projectRoot string) *ProjectConfig {
	pc := &ProjectConfig{ProjectRoot: projectRoot}
	pc.Data = pc.load()
	pc.migrateDefaultBranch()
	return pc
}

func (pc *ProjectConfig) Project() string {
	if v, ok := pc.Data["project"].(string); ok && v != "" {
		return v
	}
	return filepath.Base(pc.ProjectRoot)
}

func (pc *ProjectConfig) PortsNeeded() int {
	v := pc.Data["ports_needed"]
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

func (pc *ProjectConfig) EnvFileTarget() string {
	if v, ok := Dig(pc.Data, "env_file", "target").(string); ok {
		return v
	}
	return ".env.local"
}

func (pc *ProjectConfig) EnvFileSource() string {
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

func (pc *ProjectConfig) SetupCommands() []string {
	raw, ok := pc.Data["setup_commands"].([]any)
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
	return result
}

func (pc *ProjectConfig) StartCommand() string {
	if v, ok := pc.Data["start_command"].(string); ok {
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

func (pc *ProjectConfig) configPath() string {
	return filepath.Join(pc.ProjectRoot, ProjectConfigFile)
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

	return DeepMerge(ProjectDefaults, yamlData)
}
