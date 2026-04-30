package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/detect"
	"github.com/git-treeline/cli/internal/platform"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/templates"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var cloneIntoRE = regexp.MustCompile(`Cloning into ['"]([^'"]+)['"]`)

func init() {
	cloneCmd.DisableFlagParsing = true
	rootCmd.AddCommand(cloneCmd)
}

var cloneCmd = &cobra.Command{
	Use:   "clone <url> [directory] [-- git clone flags...]",
	Short: "Clone a repository and set up Treeline",
	Long: `Clone a git repository, then initialize and set up Treeline in one step.

All arguments after the URL are passed through to 'git clone'.
If the cloned repo already has a .treeline.yml, setup runs directly.
Otherwise, framework detection initializes the config first.

The server is NOT auto-started. Review the project, then run 'gtl start'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		for _, a := range args {
			if a == "-h" || a == "--help" {
				return cmd.Help()
			}
		}

		warnServeNotInstalled()

		if len(args) == 0 {
			return cliErr(cmd, &CliError{
				Message: "Repository URL is required.",
				Hint:    "Usage: gtl clone <url> [directory] [git clone flags...]",
			})
		}

		url := args[0]
		extraArgs := args[1:]

		fmt.Println(style.Actionf("Cloning %s", url))
		gitArgs := append([]string{"clone", url}, extraArgs...)
		gitCmd := exec.Command("git", gitArgs...)
		gitCmd.Stdout = os.Stdout
		var stderrBuf bytes.Buffer
		gitCmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}

		targetDir := parseCloneDestination(stderrBuf.String())
		if targetDir == "" {
			targetDir = inferDirectory(url, extraArgs)
		}

		absPath, err := filepath.Abs(targetDir)
		if err != nil {
			return err
		}
		if st, err := os.Stat(absPath); err != nil || !st.IsDir() {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Cloned repository not found at %s", absPath),
				Hint:    "The git clone succeeded but the expected directory is missing. Check the repo name.",
			})
		}

		uc := config.LoadUserConfig("")
		if !uc.Exists() {
			if err := uc.Init(); err != nil {
				return err
			}
			fmt.Println(style.Actionf("Created user config at %s", platform.ConfigFile()))
		}

		configPath := filepath.Join(absPath, config.ProjectConfigFile)
		if _, err := os.Stat(configPath); err != nil {
			fmt.Println(style.Actionf("No .treeline.yml found, detecting framework..."))
			project := filepath.Base(absPath)
			templateDB := project + "_development"
			detection := detect.Detect(absPath)
			detection.MergeTarget = worktree.DetectDefaultBranch(absPath)
			content := templates.ForDetection(project, templateDB, detection)
			if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
				return fmt.Errorf("init failed: %w", err)
			}
			msg := fmt.Sprintf("Created %s for project '%s'", config.ProjectConfigFile, project)
			if detection.Framework != "unknown" {
				msg += fmt.Sprintf(" (detected: %s)", detection.Framework)
			}
			fmt.Println(style.Actionf("%s", msg))
			agentPath, err := templates.WriteAgentContext(absPath, project, detection)
			if err != nil {
				fmt.Fprintln(os.Stderr, style.Warnf("failed to write agent context: %s", err))
			} else if agentPath != "" {
				fmt.Println(style.Actionf("Agent context written to %s", agentPath))
			}
		}

		fmt.Println(style.Actionf("Running setup..."))
		s := setup.New(absPath, absPath, uc)
		if _, err := s.Run(); err != nil {
			return cliErr(cmd, errSetupFailed(err))
		}

		mainRepo := worktree.DetectMainRepo(absPath)
		pc := config.LoadProjectConfig(mainRepo)
		printSetupDiagnostics(absPath, pc)

		return nil
	},
}

func parseCloneDestination(stderr string) string {
	m := cloneIntoRE.FindStringSubmatch(stderr)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func inferDirectory(url string, extraArgs []string) string {
	args := extraArgs
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			switch arg {
			case "-o", "--origin", "--depth", "-b", "--branch", "--reference", "--config", "-c",
				"--separate-git-dir", "--server-option", "--filter", "--jobs", "--shallow-since", "--shallow-exclude":
				skipNext = true
			}
			continue
		}
		return arg
	}
	base := filepath.Base(url)
	base = strings.TrimSuffix(base, ".git")
	return base
}
