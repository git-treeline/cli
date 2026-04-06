package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var newBase string
var newPath string
var newStart bool
var newOpen bool
var newDryRun bool

func init() {
	newCmd.Flags().StringVar(&newBase, "base", "", "Base branch for the new worktree (default: current branch)")
	newCmd.Flags().StringVar(&newPath, "path", "", "Custom worktree path (default: ../<project>-<branch>)")
	newCmd.Flags().BoolVar(&newStart, "start", false, "Run commands.start after setup")
	newCmd.Flags().BoolVar(&newOpen, "open", false, "Open the worktree in the browser after setup")
	newCmd.Flags().BoolVar(&newDryRun, "dry-run", false, "Print what would happen without making changes")
	newCmd.ValidArgsFunction = completeBranches
	rootCmd.AddCommand(newCmd)
}

var newCmd = &cobra.Command{
	Use:   "new <branch>",
	Short: "Create a worktree, allocate resources, and run setup",
	Long: `Create a new git worktree for the given branch, allocate ports/databases/Redis,
and run setup commands. Combines 'git worktree add' with 'gtl setup' in one step.

If the branch already exists locally or on origin, it is checked out.
Otherwise a new branch is created from --base (or the current branch).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := worktreeGuard(cmd, args); err != nil {
			return err
		}

		if err := requireServeInstalled(); err != nil {
			return err
		}

		branch := args[0]

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		mainRepo := worktree.DetectMainRepo(cwd)
		pc := config.LoadProjectConfig(mainRepo)
		uc := config.LoadUserConfig("")
		projectName := pc.Project()

		wtPath := newPath
		if wtPath == "" {
			wtPath = uc.ResolveWorktreePath(mainRepo, projectName, branch)
		}
		if wtPath == "" {
			wtPath = filepath.Join(filepath.Dir(mainRepo), fmt.Sprintf("%s-%s", projectName, branch))
		}

		if err := ensureGitignored(mainRepo, wtPath); err != nil {
			return err
		}

		// Check if this branch is already checked out in another worktree
		if existingWT := worktree.FindWorktreeForBranch(branch); existingWT != "" {
			return errBranchAlreadyCheckedOut(branch, existingWT)
		}

		existing := worktree.BranchExists(branch)

		if newDryRun {
			if existing {
				fmt.Printf("[dry-run] Would check out existing branch '%s'\n", branch)
			} else {
				base := newBase
				if base == "" {
					base = "(current branch)"
				}
				fmt.Printf("[dry-run] Would create new branch '%s' from %s\n", branch, base)
			}
			fmt.Printf("[dry-run] Worktree path: %s\n", wtPath)
			fmt.Println("[dry-run] Would run: gtl setup")
			if newStart && pc.StartCommand() != "" {
				fmt.Printf("[dry-run] Would run: %s\n", pc.StartCommand())
			}
			return nil
		}

		if existing {
			_ = worktree.Fetch("origin", branch) // non-fatal: branch may only exist locally
			fmt.Println(style.Actionf("Checking out existing branch '%s'", branch))
			if err := worktree.Create(wtPath, branch, false, ""); err != nil {
				return err
			}
		} else {
			base := newBase
			if base == "" {
				base = worktree.CurrentBranch(".")
				if base == "" {
					base = "main"
				}
			}
			fmt.Println(style.Actionf("Creating branch '%s' from '%s'", branch, base))
			if err := worktree.Create(wtPath, branch, true, base); err != nil {
				return err
			}
		}

		fmt.Println(style.Actionf("Worktree created at %s", wtPath))
		fmt.Println(style.Actionf("Running setup..."))

		s := setup.New(wtPath, mainRepo, uc)
		s.Options.DryRun = false
		alloc, err := s.Run()
		if err != nil {
			return errSetupFailed(err)
		}

		printRouterAndTunnel(uc, projectName, alloc.Branch)

		if newOpen && alloc.Port > 0 {
			url := buildOpenURL(alloc.Port, projectName, alloc.Branch, uc.RouterDomain(), uc.RouterPort(), service.IsRunning(), service.IsPortForwardConfigured())
			fmt.Printf("Opening %s\n", url)
			_ = openBrowser(url)
		}

		if newStart {
			startCmd := pc.StartCommand()
			if startCmd == "" {
				fmt.Println(style.Warnf("--start passed but no commands.start configured in .treeline.yml"))
				return nil
			}
			fmt.Println(style.Actionf("Starting: %s", startCmd))
			return execInWorktree(wtPath, startCmd)
		}

		return nil
	},
}


// worktreeGuard returns an error if the cwd is inside a worktree rather than
// the main repo. Prevents gtl new / gtl review from creating sibling worktrees.
func worktreeGuard(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	absPath, _ := filepath.Abs(cwd)
	mainRepo := worktree.DetectMainRepo(absPath)

	resolvedAbs, _ := filepath.EvalSymlinks(absPath)
	resolvedMain, _ := filepath.EvalSymlinks(mainRepo)
	if resolvedAbs != resolvedMain {
		return &CliError{
			Message: fmt.Sprintf("You're inside worktree '%s', not the main repo.", filepath.Base(absPath)),
			Hint: fmt.Sprintf("To switch this worktree: gtl switch <branch-or-PR#>\n"+
				"  To create from main repo:  cd %s && gtl %s %s",
				mainRepo, cmd.Name(), strings.Join(args, " ")),
		}
	}
	return nil
}

func execInWorktree(dir, command string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func completeBranches(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return worktree.ListBranches(toComplete), cobra.ShellCompDirectiveNoFileComp
}
