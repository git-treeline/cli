package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/style"
)

// driftReader is the buffered reader used for interactive prompts.
// Tests replace it with a bufio.NewReader(strings.NewReader(...)) so that
// multiple prompt calls share the same buffer and reads don't get lost.
var driftReader = bufio.NewReader(os.Stdin)

// adapterFor is overridable in tests to inject a mock database adapter.
var adapterFor = database.ForAdapter

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

// checkDriftOrAbort is called by start and env-sync. It offers only Abort and
// Revert — Resolve is restricted to setup since those callers have no
// allocation to work with after the registry entry is reset.
func checkDriftOrAbort(absPath string) error {
	return checkDriftOrAbortWith(absPath, registry.New(""), false)
}

// checkDriftOrAbortForSetup is called by gtl setup. It also offers Resolve,
// which resets the registry entry so setup can reallocate under the new name.
func checkDriftOrAbortForSetup(absPath string) error {
	return checkDriftOrAbortWith(absPath, registry.New(""), true)
}

func checkDriftOrAbortWith(absPath string, reg *registry.Registry, resolveEnabled bool) error {
	yamlName, registryName, drifted := detectProjectDriftWith(absPath, reg)
	if !drifted {
		return nil
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, style.Warnf("Project name mismatch:"))
	fmt.Fprintf(os.Stderr, "    .treeline.yml says %q, registry says %q.\n\n", yamlName, registryName)
	fmt.Fprintln(os.Stderr, "  How do you want to fix this?")
	fmt.Fprintf(os.Stderr, "    [1] Abort — leave everything as-is\n")
	fmt.Fprintf(os.Stderr, "    [2] Revert — update .treeline.yml back to %q\n", registryName)
	if resolveEnabled {
		fmt.Fprintf(os.Stderr, "    [3] Resolve — keep %q, reset the registry entry and fix the database\n", yamlName)
	}
	fmt.Fprintln(os.Stderr)

	maxChoice := 2
	if resolveEnabled {
		maxChoice = 3
	}

	switch promptChoice(maxChoice) {
	case 2:
		if err := revertProjectInYAML(absPath, registryName); err != nil {
			return fmt.Errorf("reverting project name: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Reverted project to %q in .treeline.yml.\n\n", registryName)
		return nil
	case 3:
		return resolveRegistryDrift(absPath, yamlName, registryName, reg)
	default:
		return &CliError{
			Message: fmt.Sprintf("Project name mismatch: .treeline.yml=%q, registry=%q.", yamlName, registryName),
			Hint:    "Run 'gtl setup' to resolve.",
		}
	}
}

type dbResolutionKind int

const (
	dbLeave  dbResolutionKind = iota
	dbRename dbResolutionKind = iota
	dbDrop   dbResolutionKind = iota
)

// resolveRegistryDrift collects all decisions about the database upfront,
// shows a plan summary, confirms once, then executes: DB action first so that
// a failure leaves the registry entry intact and the user can retry.
func resolveRegistryDrift(absPath, yamlName, registryName string, reg *registry.Registry) error {
	alloc := reg.Find(absPath)
	if alloc == nil {
		return nil
	}

	oldDB := registry.GetString(alloc, "database")
	adapterName := registry.GetString(alloc, "database_adapter")

	dbAction := dbLeave
	var oldDBPath, newDB, newDBPath string

	if oldDB != "" {
		pc := config.LoadProjectConfig(absPath)
		adapter, err := adapterFor(adapterName, pc.DatabaseConnArgs())
		if err != nil {
			return fmt.Errorf("opening database adapter: %w", err)
		}
		oldDBPath = oldDB
		if adapterName == "sqlite" {
			oldDBPath = filepath.Join(absPath, oldDB)
		}

		exists, err := adapter.Exists(oldDBPath)
		if err != nil {
			return fmt.Errorf("checking database %s: %w", oldDB, err)
		}
		if exists {
			if strings.HasPrefix(oldDB, registryName) {
				newDB = yamlName + oldDB[len(registryName):]
				newDBPath = newDB
				if adapterName == "sqlite" {
					newDBPath = filepath.Join(absPath, newDB)
				}
				fmt.Fprintln(os.Stderr)
				fmt.Fprintf(os.Stderr, "  Database found: %s\n", oldDB)
				fmt.Fprintf(os.Stderr, "    [1] Rename to %s (keeps your data)\n", newDB)
				fmt.Fprintf(os.Stderr, "    [2] Drop and recreate fresh\n")
				fmt.Fprintln(os.Stderr)
				switch promptChoice(2) {
				case 1:
					dbAction = dbRename
				case 2:
					dbAction = dbDrop
				default:
					return &CliError{Message: "Aborted — no valid choice entered."}
				}
			} else {
				fmt.Fprintln(os.Stderr)
				fmt.Fprintf(os.Stderr, "  Database found: %s\n", oldDB)
				fmt.Fprintf(os.Stderr, "    [1] Drop it (a fresh one will be created on setup)\n")
				fmt.Fprintf(os.Stderr, "    [2] Leave it as-is\n")
				fmt.Fprintln(os.Stderr)
				switch promptChoice(2) {
				case 1:
					dbAction = dbDrop
				case 2:
					dbAction = dbLeave
				default:
					return &CliError{Message: "Aborted — no valid choice entered."}
				}
			}
		}
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  Plan:")
	fmt.Fprintf(os.Stderr, "    • Reset registry entry for this worktree\n")
	switch dbAction {
	case dbRename:
		fmt.Fprintf(os.Stderr, "    • Rename database: %s → %s\n", oldDB, newDB)
	case dbDrop:
		fmt.Fprintf(os.Stderr, "    • Drop database %s (a fresh one will be created on setup)\n", oldDB)
	}
	fmt.Fprintln(os.Stderr)

	if !promptProceed("  Proceed?") {
		return &CliError{Message: "Aborted."}
	}

	if dbAction != dbLeave {
		pc := config.LoadProjectConfig(absPath)
		adapter, err := adapterFor(adapterName, pc.DatabaseConnArgs())
		if err != nil {
			return fmt.Errorf("opening database adapter: %w", err)
		}
		switch dbAction {
		case dbRename:
			fmt.Printf("==> Renaming database %s → %s\n", oldDB, newDB)
			if err := adapter.Rename(oldDBPath, newDBPath); err != nil {
				return fmt.Errorf("renaming database: %w", err)
			}
		case dbDrop:
			fmt.Printf("==> Dropping database %s\n", oldDB)
			if err := adapter.Drop(oldDBPath); err != nil {
				return fmt.Errorf("dropping database %s: %w", oldDB, err)
			}
		}
	}

	fmt.Println("==> Resetting registry entry")
	if _, err := reg.Release(absPath); err != nil {
		return fmt.Errorf("releasing registry entry: %w", err)
	}

	fmt.Fprintln(os.Stderr)
	return nil
}

// revertProjectInYAML writes the registry project name back to .treeline.yml.
func revertProjectInYAML(absPath, registryName string) error {
	pc := config.LoadProjectConfig(absPath)
	return pc.SetProject(registryName)
}

// promptChoice reads a numeric choice in [1, max] from driftReader.
// Returns 0 if the input is invalid or out of range. The prompt is written to
// stderr so it stays visible when the caller's stdout is piped — the drift menu
// it belongs to is on stderr too.
func promptChoice(max int) int {
	fmt.Fprintf(os.Stderr, "  Enter choice [1-%d]: ", max)
	line, _ := driftReader.ReadString('\n')
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &n); err == nil && n >= 1 && n <= max {
		return n
	}
	return 0
}

// promptProceed asks a default-NO [y/N] confirmation, matching the tool-wide
// convention (internal/confirm). The prompt is written to stderr — the same
// stream as the surrounding drift menu — so piping stdout doesn't hide the
// question. Reads from driftReader so tests can drive it.
func promptProceed(message string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", message)
	line, err := driftReader.ReadString('\n')
	if err != nil && line == "" {
		return false // EOF with no input — treat as abort
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes"
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
	fmt.Printf("  To fix: run 'gtl setup' and choose option [3] to resolve.\n")
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
