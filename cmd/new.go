package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/detect"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var newBase string
var newPath string
var newStart bool
var newOpen bool
var newDryRun bool
var newForce bool

func init() {
	newCmd.Flags().StringVar(&newBase, "base", "", "Base branch for the new worktree (default: current branch)")
	newCmd.Flags().StringVar(&newPath, "path", "", "Custom worktree path (default: ../<project>-<branch>)")
	newCmd.Flags().BoolVar(&newStart, "start", false, "Run commands.start after setup")
	newCmd.Flags().BoolVar(&newOpen, "open", false, "Open the worktree in the browser after setup")
	newCmd.Flags().BoolVar(&newDryRun, "dry-run", false, "Print what would happen without making changes")
	newCmd.Flags().BoolVarP(&newForce, "force", "f", false, "Skip confirmation when creating from inside a worktree")
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
		branch := args[0]

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		absPath, _ := filepath.Abs(cwd)
		mainRepo := worktree.DetectMainRepo(absPath)
		pc := config.LoadProjectConfig(mainRepo)
		uc := config.LoadUserConfig("")

		if isInWorktree(absPath, mainRepo) && !newForce && !newDryRun {
			wtPath := resolveNewWorktreePath(mainRepo, pc.Project(), branch, uc)

			fmt.Println()
			fmt.Printf("You're in worktree '%s'. The new worktree will be created at:\n", filepath.Base(absPath))
			fmt.Printf("  %s\n\n", wtPath)
			fmt.Println("You'll need to open it separately in your editor.")
			if !confirm.Prompt("Continue?", false, nil) {
				return nil
			}
			fmt.Println()
		}

		// Zero-config: if no .treeline.yml, check if this is a server project
		if !pc.Exists() {
			det := detect.Detect(mainRepo)
			if det.IsServerFramework() {
				fmt.Printf("Detected: %s application\n", det.Framework)
				fmt.Println()
				fmt.Println("This project doesn't have a .treeline.yml yet.")
				if confirm.Prompt("Set up port allocation and server management?", true, nil) {
					fmt.Println()
					if err := runInitInteractive(mainRepo, det); err != nil {
						return err
					}
					pc = config.LoadProjectConfig(mainRepo)
				} else {
					// User declined — create worktree only, no allocation
					return createWorktreeOnly(mainRepo, branch, uc, pc)
				}
			} else {
				// Not a server project — create worktree only, no allocation
				return createWorktreeOnly(mainRepo, branch, uc, pc)
			}
		}

		warnServeNotInstalled()

		projectName := pc.Project()
		wtPath := resolveNewWorktreePath(mainRepo, projectName, branch, uc)

		if err := ensureGitignored(mainRepo, wtPath); err != nil {
			return err
		}

		// If the branch is already in a worktree, ensure it has an allocation
		// and treat the command as resumable.
		if existingWT := worktree.FindWorktreeForBranch(branch); existingWT != "" {
			fmt.Println(style.Actionf("Branch '%s' already checked out at %s", branch, existingWT))
			reg := registry.New("")
			alloc := reg.Find(existingWT)

			if alloc == nil {
				fmt.Println(style.Actionf("No allocation found — running setup..."))
				s := setup.New(existingWT, mainRepo, uc)
				if _, err := s.Run(); err != nil {
					return cliErr(cmd, errSetupFailed(err))
				}
				reg = registry.New("")
				alloc = reg.Find(existingWT)
			}

			if alloc != nil {
				ports := format.GetPorts(format.Allocation(alloc))
				fmt.Printf("  Path:     %s\n", existingWT)
				if len(ports) > 0 {
					fmt.Printf("  Port:     %s\n", format.JoinInts(ports, ", "))
					printLocalAndRouter(uc, projectName, branch, ports[0])
				}

				if newOpen && len(ports) > 0 {
					url := buildOpenURL(ports[0], projectName, branch, uc.RouterDomain(), uc.RouterPort(), service.IsRunning(), service.IsPortForwardConfigured())
					fmt.Printf("Opening %s\n", url)
					_ = openBrowser(url)
				}
			}

			if newStart {
				startCmd := pc.StartCommand()
				if startCmd == "" {
					fmt.Println(style.Warnf("--start passed but no commands.start configured in .treeline.yml"))
					return nil
				}
				fmt.Println(style.Actionf("Starting: %s", startCmd))
				return execInWorktree(existingWT, startCmd)
			}

			fmt.Printf("\n  cd %s\n", existingWT)
			return nil
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
			return cliErr(cmd, errSetupFailed(err))
		}

		if newOpen && alloc.Port > 0 {
			url := buildOpenURL(alloc.Port, projectName, alloc.Branch, uc.RouterDomain(), uc.RouterPort(), service.IsRunning(), service.IsPortForwardConfigured())
			fmt.Printf("Opening %s\n", url)
			_ = openBrowser(url)
		}

		if newStart {
			pc = config.LoadProjectConfig(wtPath)
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

// createWorktreeOnly creates a worktree without any port/database allocation.
// Used for non-server projects or when user declines full setup.
func createWorktreeOnly(mainRepo, branch string, uc *config.UserConfig, pc *config.ProjectConfig) error {
	wtPath := resolveNewWorktreePath(mainRepo, pc.Project(), branch, uc)

	if existingWT := worktree.FindWorktreeForBranch(branch); existingWT != "" {
		fmt.Println(style.Actionf("Branch '%s' already checked out at %s", branch, existingWT))
		fmt.Println()
		fmt.Printf("  cd %s\n", existingWT)
		return nil
	}

	if err := ensureGitignored(mainRepo, wtPath); err != nil {
		return err
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
		fmt.Println("[dry-run] No allocation (non-server project)")
		return nil
	}

	if existing {
		_ = worktree.Fetch("origin", branch)
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
	fmt.Println()
	fmt.Printf("  cd %s\n", wtPath)
	return nil
}

// resolveNewWorktreePath returns the target path for a new worktree, applying
// --path override, user config template, or the default sibling layout.
func resolveNewWorktreePath(mainRepo, projectName, branch string, uc *config.UserConfig) string {
	if newPath != "" {
		return newPath
	}
	if p := uc.ResolveWorktreePath(mainRepo, projectName, branch); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(mainRepo), fmt.Sprintf("%s-%s", projectName, branch))
}

// runInitInteractive runs the init flow to create .treeline.yml.
// This is a simplified version that just creates the config file.
func runInitInteractive(mainRepo string, det *detect.Result) error {
	return runInitForNew(mainRepo, det)
}
