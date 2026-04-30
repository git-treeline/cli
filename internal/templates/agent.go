package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/detect"
)

const agentSectionHeader = "## Git Treeline"

// WriteAgentContext writes or appends a treeline section to AGENTS.md.
// If AGENTS.md exists, appends there. If only CLAUDE.md exists, appends there.
// If neither exists, creates AGENTS.md. Returns the path written to.
func WriteAgentContext(root, project string, det *detect.Result) (string, error) {
	agentsMD := filepath.Join(root, "AGENTS.md")
	claudeMD := filepath.Join(root, "CLAUDE.md")

	if fileExists(agentsMD) {
		return appendSection(agentsMD, project, det)
	}
	if fileExists(claudeMD) {
		return appendSection(claudeMD, project, det)
	}

	return writeNewAgentsMD(agentsMD, project, det)
}

func writeNewAgentsMD(path, project string, det *detect.Result) (string, error) {
	content := agentSectionHeader + "\n\n" + buildAgentContent(project, det)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func appendSection(path, project string, det *detect.Result) (string, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if strings.Contains(string(existing), agentSectionHeader) {
		return path, nil
	}

	section := "\n" + agentSectionHeader + "\n\n" + buildAgentContent(project, det)

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
	b.WriteString("This project uses git-treeline for port and resource allocation.\n")
	b.WriteString("**Do not assume port 3000.** Ports are dynamically allocated per worktree.\n\n")
	b.WriteString("- After creating a worktree, run `gtl setup .` to allocate ports and configure the environment.\n")
	b.WriteString("- Get the allocated port: `gtl port`\n")
	b.WriteString("- Full allocation details: `gtl status --json`\n")
	b.WriteString("- Check if services are running: `gtl status --check`\n")

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

	fmt.Fprintf(&b, "- Allocated env vars: %s in `%s`\n", strings.Join(envVars, ", "), envTarget(det))
	b.WriteString("- Inspect the env file: `gtl env` (add `--json` for structured output)\n")
	b.WriteString("- Start and wait for readiness: `gtl start --await`\n")
	b.WriteString("- Resolve another project's URL: `gtl resolve <project> --json`\n")

	if hint := PortHint(det); hint != "" {
		b.WriteString("\n### Port wiring\n\n")
		b.WriteString(hint)
		b.WriteString("\n")
	}

	return b.String()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
