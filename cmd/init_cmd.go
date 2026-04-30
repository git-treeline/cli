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
	"github.com/git-treeline/git-treeline/internal/editor"
	"github.com/git-treeline/git-treeline/internal/platform"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/style"
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
			return cliErr(cmd, &CliError{
				Message: ".treeline.yml already exists.",
				Hint:    "Edit the existing file, or delete it and re-run 'gtl init'.",
			})
		}

		uc := config.LoadUserConfig("")
		if !uc.Exists() {
			if err := uc.Init(); err != nil {
				return err
			}
			fmt.Println(style.Actionf("Created user config at %s", platform.ConfigFile()))
		}

		cwd, _ := os.Getwd()
		detection := detect.Detect(cwd)
		detection.MergeTarget = worktree.DetectDefaultBranch(cwd)
		project := initProject
		if project == "" {
			project = defaultProjectName(cwd, detection)
		}

		templateDB := initTemplateDB
		if templateDB == "" {
			templateDB = defaultTemplateDB(project, detection)
		}

		if len(detection.EnvFiles) > 1 {
			idx := confirm.Select(
				"Found multiple env files:",
				detection.EnvFiles, 0, nil,
			)
			detection.EnvFile = detection.EnvFiles[idx]
		} else if len(detection.EnvFiles) == 1 {
			if confirm.Prompt(fmt.Sprintf("Found %s — use as env file source?", detection.EnvFiles[0]), false, nil) {
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

		msg := fmt.Sprintf("Created %s for project '%s'", config.ProjectConfigFile, project)
		if detection.Framework != "unknown" {
			msg += fmt.Sprintf(" (detected: %s)", detection.Framework)
		}
		fmt.Println(style.Actionf("%s", msg))

		if !initSkipAgentConfig {
			agentPath, err := templates.WriteAgentContext(cwd, project, detection)
			if err != nil {
				fmt.Fprintln(os.Stderr, style.Warnf("failed to write agent context: %s", err))
			} else if agentPath != "" {
				fmt.Println(style.Actionf("Agent context written to %s", agentPath))
			}
		}

		if uc.EditorName() == "" {
			if detected := editor.DetectEditor(); detected != "" {
				uc.SetEditorName(detected)
				if err := uc.Save(); err != nil {
					fmt.Fprintln(os.Stderr, style.Warnf("failed to save editor name: %s", err))
				} else if info := editor.LookupEditor(detected); info != nil {
					fmt.Println(style.Actionf("Detected editor: %s", info.Display))
				}
			}
		}

		hookPath, err := templates.InstallPostCheckoutHook(cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("failed to install hook: %s", err))
		} else if hookPath != "" {
			fmt.Println(style.Actionf("Hook installed at %s", hookPath))
		}

		diags := templates.Diagnose(detection)
		for _, d := range diags {
			fmt.Println()
			if d.Level == "warn" {
				fmt.Println(style.Warn.Render("Warning:"))
			}
			for _, line := range splitLines(d.Message) {
				fmt.Printf("  %s\n", line)
			}
		}

		base := uc.PortBase()
		routerPort := uc.RouterPort()
		switch classifyPortConfig(base, routerPort) {
		case "conflict":
			fmt.Println()
			fmt.Println(style.Warnf("port.base (%d) conflicts with router.port (%d).", base, routerPort))
			fmt.Println(style.Dimf("  The router needs its own port. Fix: gtl config set port.base %d", routerPort+1))
		case "common_dev_port":
			fmt.Println()
			fmt.Println(style.Warnf("port.base is %d — a common framework default.", base))
			fmt.Println(style.Dimf("  Port 3000 should stay free for the proxy so third-party services"))
			fmt.Println(style.Dimf("  (OAuth, Mapbox, Stripe) work across branches without reconfiguration."))
			fmt.Println(style.Dimf("  Port %d is the router. Default base is 3002.", routerPort))
			fmt.Println(style.Dimf("  Fix: gtl config set port.base 3002"))
			fmt.Println(style.Dimf("  See: https://git-treeline.dev/docs/port-preservation"))
		}

		fmt.Println()
		fmt.Println(style.Bold.Render("Next steps:"))
		if !proxy.IsCAInstalled() {
			fmt.Printf("  %s   HTTPS router (one-time, requires sudo)\n", style.Cmd("gtl serve install"))
		}
		fmt.Printf("  %s           Allocate ports for this worktree\n", style.Cmd("gtl setup"))

		openInEditor(path)
		return nil
	},
}

func splitLines(s string) []string {
	return strings.Split(s, "\n")
}

// defaultTemplateDB returns the conventional development database name
// for the detected framework: {project}_dev for Phoenix, {project}_development
// for Rails and everything else.
func defaultTemplateDB(project string, det *detect.Result) string {
	if det != nil && det.DBTemplate != "" {
		return det.DBTemplate
	}
	dbProject := databaseIdentifierName(project)
	if det != nil && det.Framework == "phoenix" {
		return dbProject + "_dev"
	}
	return dbProject + "_development"
}

func databaseIdentifierName(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
		if valid {
			b.WriteRune(r)
			lastUnderscore = r == '_'
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "app"
	}
	first := result[0]
	if (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_' {
		return result
	}
	return "db_" + result
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

// runInitForNew is called from gtl new when no .treeline.yml exists.
// It creates a minimal config without the full interactive flow.
func runInitForNew(mainRepo string, det *detect.Result) error {
	path := filepath.Join(mainRepo, config.ProjectConfigFile)

	// Double-check file doesn't exist (race protection)
	if _, err := os.Stat(path); err == nil {
		fmt.Println(style.Actionf("Found existing %s", config.ProjectConfigFile))
		return nil
	}

	project := defaultProjectName(mainRepo, det)
	det.MergeTarget = worktree.DetectDefaultBranch(mainRepo)
	templateDB := defaultTemplateDB(project, det)

	content := templates.ForDetection(project, templateDB, det)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}

	msg := fmt.Sprintf("Created %s for project '%s'", config.ProjectConfigFile, project)
	if det.Framework != "unknown" {
		msg += fmt.Sprintf(" (detected: %s)", det.Framework)
	}
	fmt.Println(style.Actionf("%s", msg))
	return nil
}

func defaultProjectName(root string, det *detect.Result) string {
	if det != nil && det.ProjectName != "" {
		return det.ProjectName
	}
	return filepath.Base(root)
}
