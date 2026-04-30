package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/git-treeline/cli/internal/style"
	"github.com/spf13/cobra"
)

// CliError is a user-facing error with optional remediation hint and docs link.
// The root command's error handler formats these consistently.
type CliError struct {
	Message string
	Hint    string
	DocsURL string
}

func (e *CliError) Error() string {
	return e.Message
}

// cliErr marks the command to suppress usage output and returns the error.
// Use this for domain/state errors where the user invoked the command correctly
// but something in the environment prevents success. Cobra's default usage
// display remains active for invocation errors (wrong args, invalid flags).
func cliErr(cmd *cobra.Command, err error) error {
	if err != nil {
		cmd.SilenceUsage = true
	}
	return err
}

// formatCliError writes a structured error to stderr. Regular errors get a
// plain message; CliErrors get the hint and docs link rendered below.
func formatCliError(err error) {
	var ce *CliError
	if errors.As(err, &ce) {
		fmt.Fprintln(os.Stderr, style.Errf("%s", ce.Message))
		if ce.Hint != "" {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  "+style.Dimf("%s", ce.Hint))
		}
		if ce.DocsURL != "" {
			fmt.Fprintln(os.Stderr, "  "+style.Link(ce.DocsURL))
		}
		return
	}
	fmt.Fprintln(os.Stderr, style.Errf("%s", err))
}

// --- Shared error constructors for common patterns ---

func errNoAllocation(path string) error {
	return &CliError{
		Message: fmt.Sprintf("No allocation found for %s", path),
		Hint:    "Run 'gtl setup' in this directory first.",
	}
}

func errNoAllocationNoPorts(path string) error {
	return &CliError{
		Message: fmt.Sprintf("Allocation for %s exists but has no ports.", path),
		Hint:    "Re-run 'gtl setup' to reallocate ports.",
	}
}

func errBranchNotFound(branch string) error {
	return &CliError{
		Message: fmt.Sprintf("Branch '%s' not found on remote.", branch),
		Hint:    "The PR may be merged with the branch deleted. Check with 'git fetch --prune'.",
	}
}

func errNotInWorktree() error {
	return &CliError{
		Message: "You're in the main repo, not a worktree.",
		Hint:    "Run 'gtl switch <branch>' from here, or 'cd' into a worktree directory.",
	}
}

func errNoStartCommand() error {
	return &CliError{
		Message: "No commands.start configured in .treeline.yml",
		Hint:    "Add a 'commands.start' key to your .treeline.yml, e.g.:\n  commands:\n    start: bin/dev",
	}
}

func errServerAlreadyRunning() error {
	return &CliError{
		Message: "Server is already running.",
		Hint:    "Use 'gtl restart' to restart it, or 'gtl stop' first.",
	}
}

func errNoProjectConfig() error {
	return &CliError{
		Message: "No .treeline.yml found.",
		Hint:    "Run 'gtl init' to create one.",
	}
}

func errSetupFailed(inner error) error {
	return &CliError{
		Message: fmt.Sprintf("Setup failed: %s", inner),
		Hint:    "Fix the issue above and re-run 'gtl setup'.",
	}
}

func errInvalidPort(raw string) error {
	return &CliError{
		Message: fmt.Sprintf("Invalid port: %s", raw),
		Hint:    "Port must be a number between 1 and 65535.",
	}
}

func errNoDatabaseConfigured() error {
	return &CliError{
		Message: "No database configured for this worktree.",
		Hint:    "Add a 'database' section to .treeline.yml and re-run 'gtl setup'.",
	}
}

func errMutuallyExclusive(flags string) error {
	return &CliError{
		Message: fmt.Sprintf("%s are mutually exclusive.", flags),
		Hint:    "Use only one.",
	}
}
