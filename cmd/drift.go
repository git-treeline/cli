package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/style"
)

// detectProjectDrift compares the project name in .treeline.yml against the
// registry allocation. Returns the YAML name, registry name, and whether they
// differ. No drift is reported when no allocation exists yet.
func detectProjectDrift(absPath string) (yamlName, registryName string, drifted bool) {
	return detectProjectDriftWith(absPath, registry.New(""))
}

// detectProjectDriftWith is the testable core — accepts an explicit registry.
func detectProjectDriftWith(absPath string, reg *registry.Registry) (yamlName, registryName string, drifted bool) {
	pc := config.LoadProjectConfig(absPath)
	yamlName = pc.Project()

	alloc := reg.Find(absPath)
	if alloc == nil {
		return yamlName, "", false
	}
	registryName = registry.GetString(alloc, "project")
	if registryName == "" {
		return yamlName, "", false
	}
	return yamlName, registryName, yamlName != registryName
}

// checkDriftOrAbort detects project name drift and prompts the user to revert
// .treeline.yml. Returns nil if there's no drift or the user accepted the
// revert. Returns a non-nil error (suitable for cliErr) if the user declined
// — the caller should not proceed.
func checkDriftOrAbort(absPath string) error {
	yamlName, registryName, drifted := detectProjectDrift(absPath)
	if !drifted {
		return nil
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, style.Warnf("Project name mismatch:"))
	fmt.Fprintf(os.Stderr, "    .treeline.yml says %q, registry says %q.\n\n", yamlName, registryName)
	fmt.Fprintln(os.Stderr, "  All routing, databases, and resolve links use", fmt.Sprintf("%q.", registryName))
	fmt.Fprintln(os.Stderr)

	if promptDefaultYes(fmt.Sprintf("  Revert .treeline.yml to %q?", registryName)) {
		if err := revertProjectInYAML(absPath, registryName); err != nil {
			return fmt.Errorf("reverting project name: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Reverted project to %q in .treeline.yml.\n\n", registryName)
		return nil
	}

	return &CliError{
		Message: fmt.Sprintf("Project name mismatch: .treeline.yml=%q, registry=%q.", yamlName, registryName),
		Hint: fmt.Sprintf("To rename the project, release all worktrees first:\n"+
			"    gtl release --all --project %s\n"+
			"  Then update .treeline.yml and run gtl setup.", registryName),
	}
}

// revertProjectInYAML writes the registry project name back to .treeline.yml.
func revertProjectInYAML(absPath, registryName string) error {
	pc := config.LoadProjectConfig(absPath)
	return pc.SetProject(registryName)
}

// promptDefaultYes asks a [Y/n] question where Enter defaults to yes.
func promptDefaultYes(message string) bool {
	fmt.Printf("%s [Y/n] ", message)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer == "" || answer == "y" || answer == "yes" {
			return true
		}
	}
	return false
}

// doctorProjectDrift reports project name drift as a diagnostic finding.
// Returns true if drift was detected.
func doctorProjectDrift(absPath string) bool {
	return doctorProjectDriftWith(absPath, registry.New(""))
}

func doctorProjectDriftWith(absPath string, reg *registry.Registry) bool {
	yamlName, registryName, drifted := detectProjectDriftWith(absPath, reg)
	if !drifted {
		return false
	}
	fmt.Println("\nProject")
	doctorLine("Name drift", fmt.Sprintf("⚠ .treeline.yml=%q, registry=%q", yamlName, registryName))
	fmt.Println("  Routing, databases, and resolve links use", fmt.Sprintf("%q.", registryName))
	fmt.Printf("  To fix: revert project: in .treeline.yml to %q\n", registryName)
	return true
}

// doctorProjectDriftJSON returns drift info for JSON doctor output, or nil.
func doctorProjectDriftJSON(absPath string) map[string]string {
	return doctorProjectDriftJSONWith(absPath, registry.New(""))
}

func doctorProjectDriftJSONWith(absPath string, reg *registry.Registry) map[string]string {
	yamlName, registryName, drifted := detectProjectDriftWith(absPath, reg)
	if !drifted {
		return nil
	}
	return map[string]string{
		"status":        "drift",
		"yaml_project":  yamlName,
		"registry_name": registryName,
	}
}
