package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/confirm"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/github"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var reviewPath string
var reviewStart bool
var reviewOpen bool

func init() {
	reviewCmd.Flags().StringVar(&reviewPath, "path", "", "Custom worktree path (default: ../<project>-pr-<number>)")
	reviewCmd.Flags().BoolVar(&reviewStart, "start", false, "Run commands.start after setup")
	reviewCmd.Flags().BoolVar(&reviewOpen, "open", false, "Open the worktree in the browser after setup")
	reviewCmd.ValidArgsFunction = completePRs
	rootCmd.AddCommand(reviewCmd)
}

var reviewCmd = &cobra.Command{
	Use:   "review <PR#>",
	Short: "Check out a pull request into a worktree and run setup",
	Long: `Fetch a GitHub pull request branch, create a worktree for it, allocate
resources, and run setup. Requires the gh CLI (https://cli.github.com).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		warnServeNotInstalled()

		prNumber, err := strconv.Atoi(args[0])
		if err != nil {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Invalid PR number: %s", args[0]),
				Hint:    "Pass the numeric PR number, e.g. 'gtl review 42'.",
			})
		}

		fmt.Printf("==> Looking up PR #%d...\n", prNumber)
		pr, err := github.LookupPR(prNumber)
		if err != nil {
			return err
		}

		branch := pr.HeadRefName
		fmt.Printf("==> PR #%d → branch '%s'\n", prNumber, branch)

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		absPath, _ := filepath.Abs(cwd)
		mainRepo := worktree.DetectMainRepo(absPath)

		if isInWorktree(absPath, mainRepo) {
			currentBranch := worktree.CurrentBranch(absPath)
			branchLabel := currentBranch
			if branchLabel == "" {
				branchLabel = "(detached)"
			}
			fmt.Println()
			fmt.Printf("You're in worktree '%s' (branch: %s).\n", filepath.Base(absPath), branchLabel)
			if !confirm.Prompt(fmt.Sprintf("Switch to PR #%d (branch: %s)?", prNumber, branch), false, nil) {
				return nil
			}
			fmt.Println()
			if err := switchWorktreeBranch(absPath, mainRepo, branch, false); err != nil {
				return cliErr(cmd, err)
			}

			pc := config.LoadProjectConfig(absPath)
			uc := config.LoadUserConfig("")
			projectName := pc.Project()
			reg := registry.New("")
			alloc := reg.Find(absPath)
			ports := format.GetPorts(format.Allocation(alloc))
			primaryPort := 0
			if len(ports) > 0 {
				primaryPort = ports[0]
			}
			printLocalAndRouter(uc, projectName, branch, primaryPort)

			if reviewOpen {
				if alloc != nil {
					if len(ports) > 0 {
						url := buildOpenURL(ports[0], projectName, branch, uc.RouterDomain(), uc.RouterPort(), service.IsRunning(), service.IsPortForwardConfigured())
						fmt.Printf("Opening %s\n", url)
						_ = openBrowser(url)
					}
				}
			}

			if reviewStart {
				startCmd := pc.StartCommand()
				if startCmd == "" {
					fmt.Println(style.Warnf("--start passed but no commands.start configured in .treeline.yml"))
					return nil
				}
				fmt.Println(style.Actionf("Starting: %s", startCmd))
				return execInWorktree(absPath, startCmd)
			}

			return nil
		}

		pc := config.LoadProjectConfig(mainRepo)
		uc := config.LoadUserConfig("")
		projectName := pc.Project()

		wtPath := reviewPath
		if wtPath == "" {
			wtPath = uc.ResolveWorktreePath(mainRepo, projectName, branch)
		}
		if wtPath == "" {
			wtPath = filepath.Join(filepath.Dir(mainRepo), fmt.Sprintf("%s-pr-%d", projectName, prNumber))
		}

		if err := ensureGitignored(mainRepo, wtPath); err != nil {
			return err
		}

		// If the branch is already in a worktree, ensure it has an allocation
		// and treat the command as resumable rather than a dead end.
		if existing := worktree.FindWorktreeForBranch(branch); existing != "" {
			fmt.Printf("==> Branch '%s' already checked out at %s\n", branch, existing)
			reg := registry.New("")
			alloc := reg.Find(existing)

			if alloc == nil {
				fmt.Println("==> No allocation found — running setup...")
				s := setup.New(existing, mainRepo, uc)
				if _, err := s.Run(); err != nil {
					return cliErr(cmd, errSetupFailed(err))
				}
				reg = registry.New("")
				alloc = reg.Find(existing)
			}

			if alloc != nil {
				printExistingAllocation(prNumber, branch, existing, alloc)
				ports := format.GetPorts(format.Allocation(alloc))
				if len(ports) > 0 {
					printLocalAndRouter(uc, projectName, branch, ports[0])
				}

				if reviewOpen {
					if len(ports) > 0 {
						url := buildOpenURL(ports[0], projectName, branch, uc.RouterDomain(), uc.RouterPort(), service.IsRunning(), service.IsPortForwardConfigured())
						fmt.Printf("Opening %s\n", url)
						_ = openBrowser(url)
					}
				}
			}

			if reviewStart {
				startCmd := pc.StartCommand()
				if startCmd == "" {
					fmt.Println("Warning: --start passed but no commands.start configured in .treeline.yml")
					return nil
				}
				fmt.Printf("==> Starting: %s\n", startCmd)
				return execInWorktree(existing, startCmd)
			}

			fmt.Printf("\n  cd %s\n", existing)
			return nil
		}

		fmt.Printf("==> Fetching origin/%s...\n", branch)
		if err := worktree.Fetch("origin", branch); err != nil {
			return cliErr(cmd, errBranchNotFound(branch))
		}

		fmt.Printf("==> Creating worktree at %s\n", wtPath)
		if err := worktree.Create(wtPath, branch, false, ""); err != nil {
			return err
		}

		fmt.Println("==> Running setup...")
		s := setup.New(wtPath, mainRepo, uc)
		alloc, err := s.Run()
		if err != nil {
			return cliErr(cmd, errSetupFailed(err))
		}

		fmt.Println()
		fmt.Printf("PR #%d ready for review:\n", prNumber)
		fmt.Printf("  Branch:   %s\n", branch)
		fmt.Printf("  Path:     %s\n", wtPath)
		if alloc != nil {
			printLocalAndRouter(uc, projectName, alloc.Branch, alloc.Port)
		}

		if reviewOpen && alloc != nil && alloc.Port > 0 {
			url := buildOpenURL(alloc.Port, projectName, alloc.Branch, uc.RouterDomain(), uc.RouterPort(), service.IsRunning(), service.IsPortForwardConfigured())
			fmt.Printf("Opening %s\n", url)
			_ = openBrowser(url)
		}

		if reviewStart {
			pc = config.LoadProjectConfig(wtPath)
			startCmd := pc.StartCommand()
			if startCmd == "" {
				fmt.Println("Warning: --start passed but no commands.start configured in .treeline.yml")
				return nil
			}
			fmt.Printf("==> Starting: %s\n", startCmd)
			return execInWorktree(wtPath, startCmd)
		}

		return nil
	},
}

func printExistingAllocation(prNumber int, branch, path string, alloc registry.Allocation) {
	ports := format.GetPorts(format.Allocation(alloc))
	fmt.Println()
	fmt.Printf("PR #%d already has a worktree:\n", prNumber)
	fmt.Printf("  Branch:   %s\n", branch)
	fmt.Printf("  Path:     %s\n", path)
	if len(ports) > 0 {
		fmt.Printf("  Port:     %s\n", format.JoinInts(ports, ", "))
	}
}

func completePRs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []string
	if prs, err := github.ListOpenPRs(); err == nil {
		for _, pr := range prs {
			completions = append(completions, fmt.Sprintf("%d\t%s", pr.Number, pr.Title))
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
