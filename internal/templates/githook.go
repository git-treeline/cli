package templates

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const hookMarkerStart = "# --- git-treeline: auto-setup worktrees ---"
const hookMarkerEnd = "# --- end git-treeline ---"

const hookBlock = `# --- git-treeline: auto-setup worktrees ---
if command -v gtl >/dev/null 2>&1; then
  COMMON=$(git rev-parse --git-common-dir 2>/dev/null)
  GITDIR=$(git rev-parse --git-dir 2>/dev/null)
  if [ "$COMMON" != "$GITDIR" ]; then
    if gtl port >/dev/null 2>&1; then
      gtl editor refresh
    else
      gtl setup .
    fi
  fi
  gtl prune --stale --quiet >/dev/null 2>&1 &
fi
# --- end git-treeline ---
`

// hookRunScript is the shell one-liner used in lefthook/pre-commit entries.
// Same logic as hookBlock but collapsed for YAML run: fields.
const hookRunScript = `command -v gtl >/dev/null 2>&1 && { COMMON=$(git rev-parse --git-common-dir 2>/dev/null); GITDIR=$(git rev-parse --git-dir 2>/dev/null); [ "$COMMON" != "$GITDIR" ] && { gtl port >/dev/null 2>&1 && gtl editor refresh || gtl setup .; }; gtl prune --stale --quiet >/dev/null 2>&1 & } || true`

// InstallPostCheckoutHook writes a post-checkout Git hook that triggers
// gtl setup for new worktrees and gtl editor refresh for branch changes.
// Detects hook managers (husky, lefthook, pre-commit) and integrates directly.
// Appends to existing hooks/configs rather than clobbering them.
// Returns the path written to.
func InstallPostCheckoutHook(repoRoot string) (string, error) {
	_, manager := resolveHooksDir(repoRoot)

	switch manager {
	case "lefthook":
		return installLefthook(repoRoot)
	case "pre-commit":
		return installPreCommit(repoRoot)
	default:
		return installShellHook(repoRoot, manager)
	}
}

// installShellHook writes a post-checkout shell script for git or husky.
func installShellHook(repoRoot, manager string) (string, error) {
	hooksDir, _ := resolveHooksDir(repoRoot)
	hookPath := filepath.Join(hooksDir, "post-checkout")

	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", fmt.Errorf("creating hooks directory: %w", err)
	}

	existing, _ := os.ReadFile(hookPath)
	content := string(existing)

	if strings.Contains(content, hookMarkerStart) {
		content = replaceHookBlock(content)
	} else {
		if content == "" {
			content = "#!/bin/sh\n"
		} else if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + hookBlock
	}

	if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
		return "", err
	}
	return hookPath, nil
}

// installLefthook appends a git-treeline command to lefthook.yml's post-checkout section.
func installLefthook(repoRoot string) (string, error) {
	path := filepath.Join(repoRoot, "lefthook.yml")
	if !fileExists(path) {
		path = filepath.Join(repoRoot, ".lefthook.yml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}

	var config yaml.Node
	if err := yaml.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}

	if hasLefthookEntry(&config) {
		return path, nil
	}

	appendLefthookEntry(&config)

	out, err := yaml.Marshal(&config)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func hasLefthookEntry(doc *yaml.Node) bool {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return false
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return false
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == "post-checkout" {
			section := root.Content[i+1]
			if section.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j < len(section.Content)-1; j += 2 {
				if section.Content[j].Value == "commands" {
					cmds := section.Content[j+1]
					if cmds.Kind != yaml.MappingNode {
						continue
					}
					for k := 0; k < len(cmds.Content)-1; k += 2 {
						if cmds.Content[k].Value == "git-treeline" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func appendLefthookEntry(doc *yaml.Node) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return
	}

	entry := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "run"},
			{Kind: yaml.ScalarNode, Value: hookRunScript},
		},
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == "post-checkout" {
			section := root.Content[i+1]
			if section.Kind != yaml.MappingNode {
				return
			}
			for j := 0; j < len(section.Content)-1; j += 2 {
				if section.Content[j].Value == "commands" {
					cmds := section.Content[j+1]
					cmds.Content = append(cmds.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "git-treeline"},
						entry,
					)
					return
				}
			}
			// post-checkout exists but no commands key
			section.Content = append(section.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "commands"},
				&yaml.Node{
					Kind: yaml.MappingNode,
					Content: []*yaml.Node{
						{Kind: yaml.ScalarNode, Value: "git-treeline"},
						entry,
					},
				},
			)
			return
		}
	}

	// No post-checkout section at all
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "post-checkout"},
		&yaml.Node{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "commands"},
				{
					Kind: yaml.MappingNode,
					Content: []*yaml.Node{
						{Kind: yaml.ScalarNode, Value: "git-treeline"},
						entry,
					},
				},
			},
		},
	)
}

// installPreCommit appends a git-treeline local hook to .pre-commit-config.yaml.
func installPreCommit(repoRoot string) (string, error) {
	path := filepath.Join(repoRoot, ".pre-commit-config.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading .pre-commit-config.yaml: %w", err)
	}

	content := string(data)
	if strings.Contains(content, "id: git-treeline") {
		return path, nil
	}

	hookEntry := `
- repo: local
  hooks:
    - id: git-treeline
      name: git-treeline worktree setup
      entry: bash -c '` + hookRunScript + `'
      language: system
      stages: [post-checkout]
      always_run: true
`

	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += hookEntry

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "Note: run `pre-commit install --hook-type post-checkout` to activate.\n")
	return path, nil
}

// resolveHooksDir returns the directory where Git hooks live and the name
// of any hook manager in use. Returns (".git/hooks", "git") for standard setup.
func resolveHooksDir(repoRoot string) (string, string) {
	cmd := exec.Command("git", "config", "--local", "core.hooksPath")
	cmd.Dir = repoRoot
	if out, err := cmd.Output(); err == nil {
		customPath := strings.TrimSpace(string(out))
		if customPath != "" {
			if !filepath.IsAbs(customPath) {
				customPath = filepath.Join(repoRoot, customPath)
			}
			manager := detectHookManager(repoRoot, customPath)
			return customPath, manager
		}
	}

	huskyDir := filepath.Join(repoRoot, ".husky")
	if info, err := os.Stat(huskyDir); err == nil && info.IsDir() {
		return huskyDir, "husky"
	}

	if fileExists(filepath.Join(repoRoot, "lefthook.yml")) ||
		fileExists(filepath.Join(repoRoot, ".lefthook.yml")) {
		return filepath.Join(repoRoot, ".git", "hooks"), "lefthook"
	}

	if fileExists(filepath.Join(repoRoot, ".pre-commit-config.yaml")) {
		return filepath.Join(repoRoot, ".git", "hooks"), "pre-commit"
	}

	gitDir := filepath.Join(repoRoot, ".git", "hooks")
	return gitDir, "git"
}

func detectHookManager(repoRoot, hooksPath string) string {
	abs, _ := filepath.Abs(hooksPath)
	if strings.Contains(abs, ".husky") {
		return "husky"
	}

	if fileExists(filepath.Join(repoRoot, "lefthook.yml")) ||
		fileExists(filepath.Join(repoRoot, ".lefthook.yml")) {
		return "lefthook"
	}

	if fileExists(filepath.Join(repoRoot, ".pre-commit-config.yaml")) {
		return "pre-commit"
	}

	return "git"
}

func replaceHookBlock(content string) string {
	startIdx := strings.Index(content, hookMarkerStart)
	endIdx := strings.Index(content, hookMarkerEnd)
	if startIdx < 0 || endIdx < 0 {
		return content
	}
	endIdx += len(hookMarkerEnd)
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}
	return content[:startIdx] + hookBlock + content[endIdx:]
}
