package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/style"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(updateCmd)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update git-treeline to the latest version",
	Long: `Update git-treeline using the package manager it was installed with.

Detects the install channel from the binary's location:
  - Homebrew (Cellar path): runs 'brew update' then 'brew upgrade git-treeline'.
    The formula's post-install hook restarts the router automatically.
  - go install (GOBIN/GOPATH): runs 'go install github.com/git-treeline/cli@latest'.

For release-binary installs the channel can't be detected; instructions are
printed instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		exe, err := os.Executable()
		if err != nil {
			return cliErr(cmd, fmt.Errorf("locating executable: %w", err))
		}
		// os.Executable may or may not resolve the Homebrew bin symlink
		// depending on platform; resolve so Cellar detection sees the real path.
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}

		switch detectInstallMethod(exe, goBinDirs()) {
		case installHomebrew:
			fmt.Println(style.Actionf("Updating via Homebrew"))
			if err := runUpdateStep(cmd, "brew", "update"); err != nil {
				return cliErr(cmd, err)
			}
			if err := runUpdateStep(cmd, "brew", "upgrade", "git-treeline"); err != nil {
				return cliErr(cmd, err)
			}
		case installGoBin:
			fmt.Println(style.Actionf("Updating via go install"))
			if err := runUpdateStep(cmd, "go", "install", "github.com/git-treeline/cli@latest"); err != nil {
				return cliErr(cmd, err)
			}
		default:
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Cannot determine how git-treeline was installed (%s).", exe),
				Hint: "Homebrew:       brew upgrade git-treeline\n" +
					"  go install:     go install github.com/git-treeline/cli@latest\n" +
					"  Release binary: download the latest release and replace the binary.",
			})
		}

		fmt.Println(style.Successf("Updated. Run 'gtl version' to confirm."))
		return nil
	},
}

type installMethod int

const (
	installUnknown installMethod = iota
	installHomebrew
	installGoBin
)

// detectInstallMethod classifies the install channel from the (symlink-resolved)
// executable path. Homebrew installs live under a Cellar directory
// (<prefix>/Cellar/<formula>/<version>/bin); 'go install' places binaries
// directly in one of goBins.
func detectInstallMethod(exe string, goBins []string) installMethod {
	dir := filepath.Dir(exe)
	for _, part := range strings.Split(dir, string(filepath.Separator)) {
		if part == "Cellar" {
			return installHomebrew
		}
	}
	for _, b := range goBins {
		if b != "" && dir == filepath.Clean(b) {
			return installGoBin
		}
	}
	return installUnknown
}

// goBinDirs returns the directories 'go install' may have placed the binary
// in, mirroring the toolchain's resolution order: GOBIN, then GOPATH/bin,
// then the default ~/go/bin.
func goBinDirs() []string {
	var dirs []string
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		dirs = append(dirs, gobin)
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		dirs = append(dirs, filepath.Join(gopath, "bin"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}
	return dirs
}

// runUpdateStep runs an external command with output streamed to the user.
func runUpdateStep(cmd *cobra.Command, name string, args ...string) error {
	fmt.Println(style.Dimf("  $ %s %s", name, strings.Join(args, " ")))
	c := exec.Command(name, args...)
	c.Stdin = cmd.InOrStdin()
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	if err := c.Run(); err != nil {
		return fmt.Errorf("'%s %s' failed: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
