package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/spf13/cobra"
)

var linkJSON bool
var linkRestart bool

func init() {
	linkCmd.Flags().BoolVar(&linkJSON, "json", false, "Output as JSON")
	linkCmd.Flags().BoolVar(&linkRestart, "restart", false, "Deprecated: server is now always restarted if running")
	_ = linkCmd.Flags().MarkDeprecated("restart", "server is now always restarted if running")
	rootCmd.AddCommand(linkCmd)
	rootCmd.AddCommand(unlinkCmd)
}

var linkCmd = &cobra.Command{
	Use:   "link [project] [branch]",
	Short: "Link a resolve target to a specific branch",
	Long: `Override which branch a {resolve:project} token points at for this worktree.

With no arguments, lists active links.
With a project and branch, sets the link, rewrites the env file, and restarts
the supervised server if running.

Examples:
  gtl link                          # list active links
  gtl link api feature-payments     # override api -> feature-payments`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)
		reg := registry.New("")

		if len(args) == 0 {
			return listLinks(reg, absPath)
		}
		if len(args) != 2 {
			return cliErr(cmd, &CliError{
				Message: "Missing arguments.",
				Hint:    "Usage: gtl link <project> <branch>",
			})
		}

		project := args[0]
		branch := args[1]

		alloc := reg.Find(absPath)
		if alloc == nil {
			return cliErr(cmd, errNoAllocation(absPath))
		}

		target := reg.FindProjectBranch(project, branch)
		if target == nil {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("No allocation for project %q on branch %q.", project, branch),
				Hint:    "Run 'gtl setup' in that worktree first.",
			})
		}

		if err := reg.SetLink(absPath, project, branch); err != nil {
			return fmt.Errorf("setting link: %w", err)
		}

		fmt.Printf("Linked %s -> %s\n", project, branch)

		// Regenerate env file immediately so the link takes effect
		uc := config.LoadUserConfig("")
		if err := setup.RegenerateEnvFile(absPath, uc); err != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("Could not update env file: %s", err))
		} else {
			fmt.Println("Environment file updated.")
		}

		// Restart the server (or warn if we can't)
		sockPath := supervisor.SocketPath(absPath)
		if resp, err := supervisor.Send(sockPath, "restart"); err == nil && resp == "ok" {
			fmt.Println("Server restarted.")
		} else {
			fmt.Println(style.Warnf("Server not running — restart manually to pick up the new link."))
		}

		return nil
	},
}

var unlinkCmd = &cobra.Command{
	Use:   "unlink <project>",
	Short: "Remove a resolve link override",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)
		reg := registry.New("")

		project := args[0]

		links := reg.GetLinks(absPath)
		if _, ok := links[project]; !ok {
			fmt.Printf("No active link for %q\n", project)
			return nil
		}

		if err := reg.RemoveLink(absPath, project); err != nil {
			return fmt.Errorf("removing link: %w", err)
		}

		fmt.Printf("Unlinked %s (will resolve to same-branch default)\n", project)

		// Regenerate env file immediately so the unlink takes effect
		uc := config.LoadUserConfig("")
		if err := setup.RegenerateEnvFile(absPath, uc); err != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("Could not update env file: %s", err))
		} else {
			fmt.Println("Environment file updated.")
		}

		// Restart the server (or warn if we can't)
		sockPath := supervisor.SocketPath(absPath)
		if resp, err := supervisor.Send(sockPath, "restart"); err == nil && resp == "ok" {
			fmt.Println("Server restarted.")
		} else {
			fmt.Println(style.Warnf("Server not running — restart manually to pick up the change."))
		}

		return nil
	},
}

func listLinks(reg *registry.Registry, absPath string) error {
	links := reg.GetLinks(absPath)
	if len(links) == 0 {
		fmt.Println("No active links.")
		return nil
	}

	if linkJSON {
		data, err := json.MarshalIndent(links, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding links: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	keys := make([]string, 0, len(links))
	for k := range links {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		fmt.Printf("  %s -> %s\n", k, links[k])
	}
	return nil
}
