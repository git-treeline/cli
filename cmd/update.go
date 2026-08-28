package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/selfupdate"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/style"
	"github.com/spf13/cobra"
)

// fetchTimeout bounds the explicit release check in 'gtl update'. Generous
// compared to the passive notice path — the user asked for this network call.
const fetchTimeout = 10 * time.Second

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().Bool("check", false, "Report whether a newer version exists without installing it")
	updateCmd.Flags().Bool("json", false, "Output check result as JSON (implies --check)")
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update git-treeline to the latest version",
	Long: `Update git-treeline using the package manager it was installed with.

Checks the latest published release first, then detects the install channel
from the binary's location:
  - Homebrew (Cellar path): runs 'brew update' then 'brew upgrade git-treeline'.
    The formula's post-install hook restarts the router automatically.
  - go install (GOBIN/GOPATH): runs 'go install github.com/git-treeline/cli@latest'.

For release-binary installs the channel can't be detected; instructions are
printed instead.

--check reports the current and latest versions without installing anything.

Other commands print a one-line notice when a newer release exists (checked
at most once a day, never blocking). Suppress with GTL_NO_UPDATE_NOTIFY=1.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, _ := cmd.Flags().GetBool("json")
		checkOnly, _ := cmd.Flags().GetBool("check")
		checkOnly = checkOnly || asJSON

		latest, fetchErr := selfupdate.FetchLatestVersion(fetchTimeout)
		if fetchErr == nil {
			_ = selfupdate.WriteState(latest, time.Now())
		}
		outdated := selfupdate.IsNewer(Version, latest)

		if checkOnly {
			return cliErr(cmd, reportCheck(cmd, latest, fetchErr, outdated, asJSON))
		}

		if fetchErr != nil {
			fmt.Println(style.Warnf("Could not check the latest version (%v) — running the upgrade anyway.", fetchErr))
		} else if Version == "dev" {
			fmt.Println(style.Dimf("Development build; latest release is %s.", latest))
		} else if !outdated {
			fmt.Println(style.Successf("Already on the latest version (%s).", Version))
			return nil
		} else {
			fmt.Println(style.Actionf("Updating %s → %s", Version, latest))
		}

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

		return cliErr(cmd, verifyUpdated(latest))
	},
}

// reportCheck implements --check / --json: report versions, install nothing.
// Exits 0 whether or not an update exists; scripts should read the JSON
// "outdated" field rather than the exit code.
func reportCheck(cmd *cobra.Command, latest string, fetchErr error, outdated, asJSON bool) error {
	if asJSON {
		out := map[string]interface{}{
			"current":  Version,
			"latest":   nil,
			"outdated": outdated,
		}
		if latest != "" {
			out["latest"] = latest
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if fetchErr != nil {
		return &CliError{
			Message: "Could not check the latest version.",
			Hint:    "GitHub releases API unreachable: " + fetchErr.Error(),
		}
	}
	switch {
	case Version == "dev":
		fmt.Printf("Current version: %s (development build)\nLatest release:  %s\n", Version, latest)
	case outdated:
		fmt.Printf("Current version: %s\nLatest release:  %s\n", Version, latest)
		fmt.Println(style.Dimf("Run 'gtl update' to install it."))
	default:
		fmt.Println(style.Successf("Already on the latest version (%s).", Version))
	}
	return nil
}

// verifyUpdated re-runs the (now upgraded) binary at its stable path and
// confirms it reports a new version. Catches the "brew upgraded the Cellar
// but a stale binary still shadows it in PATH" failure mode instead of
// declaring success on top of it.
func verifyUpdated(latest string) error {
	stable, err := service.StableExecutablePath()
	if err != nil {
		fmt.Println(style.Successf("Updated. Run 'gtl version' to confirm."))
		return nil
	}
	out, err := exec.Command(stable, "version").Output()
	installed := parseVersionOutput(string(out))
	if err != nil || installed == "" {
		fmt.Println(style.Successf("Updated. Run 'gtl version' to confirm."))
		return nil
	}
	if installed == Version && selfupdate.IsNewer(Version, latest) {
		return &CliError{
			Message: fmt.Sprintf("Upgrade ran, but %s still reports %s (latest is %s).", stable, installed, latest),
			Hint:    "Another gtl binary may shadow it in PATH — check 'which -a gtl'.",
		}
	}
	fmt.Println(style.Successf("Updated to %s.", installed))
	return nil
}

// parseVersionOutput extracts the version from 'gtl version' output
// ("git-treeline v0.57.0").
func parseVersionOutput(out string) string {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 || fields[0] != "git-treeline" {
		return ""
	}
	return fields[1]
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
