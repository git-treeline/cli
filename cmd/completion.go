package cmd

import (
	"os"
	"strings"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(completionCmd)
}

// registryProjects returns the distinct project names present in the registry,
// filtered by the completion prefix. Used to complete <project> arguments.
func registryProjects(toComplete string) []string {
	reg := registry.New("")
	seen := map[string]bool{}
	var out []string
	for _, a := range reg.Allocations() {
		p, _ := a["project"].(string)
		if p == "" || seen[p] || !strings.HasPrefix(p, toComplete) {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// registryBranches returns the distinct branch names present in the registry,
// filtered by the completion prefix. Used to complete <branch> arguments that
// resolve against allocations rather than the local git branch list.
func registryBranches(toComplete string) []string {
	reg := registry.New("")
	seen := map[string]bool{}
	var out []string
	for _, a := range reg.Allocations() {
		b, _ := a["branch"].(string)
		if b == "" || seen[b] || !strings.HasPrefix(b, toComplete) {
			continue
		}
		seen[b] = true
		out = append(out, b)
	}
	return out
}

// registryWorktreePaths returns the worktree paths present in the registry,
// filtered by the completion prefix. Used to complete <path> arguments that
// operate on existing allocations (reallocate, registry forget).
func registryWorktreePaths(toComplete string) []string {
	reg := registry.New("")
	var out []string
	for _, a := range reg.Allocations() {
		wt, _ := a["worktree"].(string)
		if wt == "" || !strings.HasPrefix(wt, toComplete) {
			continue
		}
		out = append(out, wt)
	}
	return out
}

// completeRegistryProjects completes a single <project> argument.
func completeRegistryProjects(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return registryProjects(toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeProjectThenBranch completes <project> for the first argument and a
// registry branch for the second. Used by resolve and link.
func completeProjectThenBranch(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return registryProjects(toComplete), cobra.ShellCompDirectiveNoFileComp
	case 1:
		return registryBranches(toComplete), cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeRegistryBranch completes a single <branch> argument from the registry.
func completeRegistryBranch(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return registryBranches(toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeRegistryWorktreePaths completes <path> arguments against the worktree
// paths recorded in the registry. reallocate and registry forget accept these.
func completeRegistryWorktreePaths(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return registryWorktreePaths(toComplete), cobra.ShellCompDirectiveNoFileComp
}

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for git-treeline.

Homebrew installs completions automatically. For manual installation:

  bash:  gtl completion bash > /etc/bash_completion.d/gtl
  zsh:   gtl completion zsh > "${fpath[1]}/_gtl"
  fish:  gtl completion fish > ~/.config/fish/completions/gtl.fish`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		// The binary is installed and invoked as 'gtl' (a symlink to the
		// 'git-treeline' binary), so the completion script must register the
		// 'gtl' command name — otherwise 'gtl <tab>' does nothing. Cobra
		// derives the completion name from the root command's Name(), so we
		// temporarily override the root Use for generation and restore it.
		root := cmd.Root()
		origUse := root.Use
		root.Use = "gtl"
		defer func() { root.Use = origUse }()

		switch args[0] {
		case "bash":
			return root.GenBashCompletion(os.Stdout)
		case "zsh":
			return root.GenZshCompletion(os.Stdout)
		case "fish":
			return root.GenFishCompletion(os.Stdout, true)
		case "powershell":
			return root.GenPowerShellCompletionWithDesc(os.Stdout)
		}
		return nil
	},
}
