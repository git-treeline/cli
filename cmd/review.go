package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/github"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var reviewPath string
var reviewStart bool
var reviewOpen bool

// parsePRNumber parses a PR number argument, accepting an optional leading '#'
// (e.g. both "473" and "#473") and surrounding whitespace (e.g. a "#473 "
// pasted with a trailing space). PR numbers are positive, so zero and negative
// values are rejected.
func parsePRNumber(arg string) (int, error) {
	arg = strings.TrimSpace(arg)
	n, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
	if err != nil {
		return 0, err
	}
	if n < 1 {
		return 0, fmt.Errorf("PR number must be positive: %s", arg)
	}
	return n, nil
}

func init() {
	reviewCmd.Flags().StringVar(&reviewPath, "path", "", "Custom worktree path (default: ../<project>-pr-<number>)")
	reviewCmd.Flags().BoolVar(&reviewStart, "start", false, "Run commands.start after setup")
	reviewCmd.Flags().BoolVar(&reviewOpen, "open", false, "Open the worktree in the browser after setup")
	reviewCmd.ValidArgsFunction = completePRs
	rootCmd.AddCommand(reviewCmd)
}

var reviewCmd = &cobra.Command{
	Use:   "review <PR>",
	Short: "Check out a pull request into a worktree and run setup",
	Long: `Fetch a GitHub pull request branch, create a worktree for it, allocate
resources, and run setup. Requires the gh CLI (https://cli.github.com).

The PR may be given as a bare number or with a leading '#':
  gtl review 42
  gtl review '#42'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		warnServeNotInstalled()

		prNumber, err := parsePRNumber(args[0])
		if err != nil {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Invalid PR number: %s", args[0]),
				Hint:    "Pass the PR number, e.g. 'gtl review 42' or 'gtl review #42'.",
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
			pc := config.LoadProjectConfig(absPath)
			uc := config.LoadUserConfig("")

			currentBranch := worktree.CurrentBranch(absPath)
			branchLabel := currentBranch
			if branchLabel == "" {
				branchLabel = "(detached)"
			}
			fmt.Println()
			fmt.Printf("You're in worktree '%s' (branch: %s).\n", filepath.Base(absPath), branchLabel)
			if !confirm.Prompt(fmt.Sprintf("Switch to PR #%d (branch: %s)?", prNumber, branch), uc.ReviewSkipSwitchConfirm(), nil) {
				return nil
			}
			fmt.Println()
			if err := switchWorktreeBranch(absPath, mainRepo, branch, false); err != nil {
				return cliErr(cmd, err)
			}

			projectName := pc.Project()
			reg := registry.New("")
			alloc := reg.Find(absPath)
			ports := format.GetPorts(format.Allocation(alloc))
			primaryPort := 0
			if len(ports) > 0 {
				primaryPort = ports[0]
			}
			printLocalAndRouter(uc, projectName, branch, primaryPort)

			if alloc != nil {
				maybeOpenInBrowser(reviewOpen, uc, primaryPort, projectName, branch)
			}

			if handled, err := maybeStartServer(reviewStart, pc, absPath); handled {
				return err
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
			alloc, err := ensureWorktreeAllocation(existing, mainRepo, uc, os.Stdout)
			if err != nil {
				return cliErr(cmd, err)
			}

			if alloc != nil {
				printExistingAllocation(prNumber, branch, existing, alloc)
				ports := format.GetPorts(format.Allocation(alloc))
				if len(ports) > 0 {
					printLocalAndRouter(uc, projectName, branch, ports[0])
					maybeOpenInBrowser(reviewOpen, uc, ports[0], projectName, branch)
				}
			}

			if handled, err := maybeStartServer(reviewStart, pc, existing); handled {
				return err
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

		if alloc != nil {
			maybeOpenInBrowser(reviewOpen, uc, alloc.Port, projectName, alloc.Branch)
		}

		if handled, err := maybeStartServer(reviewStart, config.LoadProjectConfig(wtPath), wtPath); handled {
			return err
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
			// Emit the '#N' form so the supported syntax is discoverable from
			// completion; parsePRNumber accepts both '#42' and '42'.
			completions = append(completions, fmt.Sprintf("#%d\t%s", pr.Number, pr.Title))
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
