package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(completionCmd)
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
