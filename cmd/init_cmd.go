package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/confirm"
	"github.com/git-treeline/git-treeline/internal/detect"
	"github.com/git-treeline/git-treeline/internal/platform"
	"github.com/git-treeline/git-treeline/internal/templates"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var initProject string
var initTemplateDB string
var initSkipAgentConfig bool

func init() {
	initCmd.Flags().StringVar(&initProject, "project", "", "Project name")
	initCmd.Flags().StringVar(&initTemplateDB, "template-db", "", "Template database name for cloning")
	initCmd.Flags().BoolVar(&initSkipAgentConfig, "skip-agent-config", false, "Skip generating agent context files")
	rootCmd.AddCommand(initCmd)
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a .treeline.yml config file for the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := filepath.Join(".", config.ProjectConfigFile)
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintln(os.Stderr, ".treeline.yml already exists")
			os.Exit(1)
		}

		uc := config.LoadUserConfig("")
		if !uc.Exists() {
			if err := uc.Init(); err != nil {
				return err
			}
			fmt.Printf("==> Created user config at %s\n", platform.ConfigFile())
		}

		project := initProject
		if project == "" {
			cwd, _ := os.Getwd()
			project = filepath.Base(cwd)
		}

		templateDB := initTemplateDB
		if templateDB == "" {
			templateDB = project + "_development"
		}

		cwd, _ := os.Getwd()
		detection := detect.Detect(cwd)
		detection.DefaultBranch = worktree.DetectDefaultBranch(cwd)

		if len(detection.EnvFiles) > 1 {
			idx := confirm.Select(
				"==> Found multiple env files:",
				detection.EnvFiles, 0, nil,
			)
			detection.EnvFile = detection.EnvFiles[idx]
		} else if len(detection.EnvFiles) == 1 {
			if confirm.Prompt(fmt.Sprintf("==> Found %s — use as env file source?", detection.EnvFiles[0]), false, nil) {
				detection.EnvFile = detection.EnvFiles[0]
			} else {
				detection.HasEnvFile = false
				detection.EnvFile = ""
			}
		}

		content := templates.ForDetection(project, templateDB, detection)

		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}

		fmt.Printf("==> Created %s for project '%s'", config.ProjectConfigFile, project)
		if detection.Framework != "unknown" {
			fmt.Printf(" (detected: %s)", detection.Framework)
		}
		fmt.Println()
		fmt.Println()
		fmt.Printf("Allocation policy (ports, Redis) is managed in your user config:\n")
		fmt.Printf("  %s\n", platform.ConfigFile())

		if hint := templates.PortHint(detection); hint != "" {
			fmt.Println()
			fmt.Println("⚠  Port wiring required:")
			for _, line := range splitLines(hint) {
				fmt.Printf("  %s\n", line)
			}
		}

		if !initSkipAgentConfig {
			agentPath, err := templates.WriteAgentContext(cwd, project, detection)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to write agent context: %s\n", err)
			} else if agentPath != "" {
				fmt.Printf("==> Agent context written to %s\n", agentPath)
			}
		}

		openInEditor(path)
		return nil
	},
}

func splitLines(s string) []string {
	return strings.Split(s, "\n")
}

func openInEditor(path string) {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		return
	}
	_ = exec.Command(editor, path).Run()
}
