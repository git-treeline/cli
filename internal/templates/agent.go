package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/detect"
)

// WriteAgentContext generates agent context files if the project already uses
// a supported agent configuration format. Does nothing if no agent config
// directory/file is found.
func WriteAgentContext(root, project string, det *detect.Result) (string, error) {
	cursorRulesDir := filepath.Join(root, ".cursor", "rules")
	cursorDir := filepath.Join(root, ".cursor")
	claudeMD := filepath.Join(root, "CLAUDE.md")

	if dirExists(cursorRulesDir) || dirExists(cursorDir) {
		return writeCursorRule(cursorRulesDir, project, det)
	}

	if fileExists(claudeMD) {
		return appendClaudeMD(claudeMD, project, det)
	}

	return "", nil
}

func writeCursorRule(rulesDir, project string, det *detect.Result) (string, error) {
	_ = os.MkdirAll(rulesDir, 0o755)
	path := filepath.Join(rulesDir, "treeline.mdc")
	content := buildAgentContent(project, det)

	rule := fmt.Sprintf(`---
description: Git Treeline worktree resource management
globs: ["**"]
---
%s`, content)

	if err := os.WriteFile(path, []byte(rule), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func appendClaudeMD(path, project string, det *detect.Result) (string, error) {
	content := buildAgentContent(project, det)
	section := "\n## Git Treeline\n\n" + content

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}

	if _, err := f.WriteString(section); err != nil {
		_ = f.Close()
		return "", err
	}
	return path, f.Close()
}

func buildAgentContent(project string, det *detect.Result) string {
	var b strings.Builder
	b.WriteString("This project uses git-treeline for worktree isolation.\n\n")
	b.WriteString("- Check allocations: `gtl status --json`\n")
	b.WriteString("- Check running services: `gtl status --check`\n")
	b.WriteString("- Re-apply env after config changes: `gtl refresh .`\n")

	envVars := []string{"PORT"}
	switch det.Framework {
	case "nextjs":
		if det.HasPrisma {
			envVars = append(envVars, "DATABASE_URL")
		}
	case "rails":
		envVars = append(envVars, "DATABASE_NAME")
		if det.HasRedis {
			envVars = append(envVars, "REDIS_URL")
		}
	}

	envFile := det.EnvFile
	if envFile == "" {
		envFile = ".env"
	}
	fmt.Fprintf(&b, "- Allocated env vars: %s in %s\n", strings.Join(envVars, ", "), envFile)

	if hint := PortHint(det); hint != "" {
		b.WriteString("\n### Port wiring\n\n")
		b.WriteString(hint)
		b.WriteString("\n")
	}

	return b.String()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
