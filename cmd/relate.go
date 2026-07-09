package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	relateFrom string
	relateType string
	relateJSON bool

	unrelateFrom string
	unrelateJSON bool
)

func init() {
	relateCmd.Flags().StringVar(&relateFrom, "from", "", "Source endpoint (defaults to the worktree in the current directory)")
	relateCmd.Flags().StringVar(&relateType, "type", "related", "Relationship type")
	relateCmd.Flags().BoolVar(&relateJSON, "json", false, "Output as JSON")

	unrelateCmd.Flags().StringVar(&unrelateFrom, "from", "", "Source endpoint (defaults to the worktree in the current directory)")
	unrelateCmd.Flags().BoolVar(&unrelateJSON, "json", false, "Output as JSON")

	// Best-effort: the <target> also accepts owner/name#branch and paths, but
	// the bare-branch form is the common case and completes cleanly.
	relateCmd.ValidArgsFunction = completeBranches
	unrelateCmd.ValidArgsFunction = completeBranches

	rootCmd.AddCommand(relateCmd)
	rootCmd.AddCommand(unrelateCmd)
}

const refFormatHelp = `A <target> or --from may be:
  owner/name#branch   explicit repo identity and branch
  <path>              a worktree directory (repo and branch read from it)
  <branch>            a bare branch, resolved against the current repo`

var relateCmd = &cobra.Command{
	Use:   "relate <target>",
	Short: "Relate the current worktree to another (repo, branch)",
	Long: `Create a durable relationship between two lines of work, keyed by
(repo, branch) so it survives archiving and recreating either worktree.

The relationship is symmetric and idempotent — relating an already-related pair
is a no-op success.

` + refFormatHelp + `

Examples:
  gtl relate acme/api#feature-payments
  gtl relate feature-spec --from acme/web#main
  gtl relate ../other-worktree --type consumes-api`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		from, err := resolveRepoRef(relateFrom, cwd)
		if err != nil {
			return cliErr(cmd, &CliError{Message: err.Error(), Hint: refFormatHelp})
		}
		target, err := resolveRepoRef(args[0], cwd)
		if err != nil {
			return cliErr(cmd, &CliError{Message: err.Error(), Hint: refFormatHelp})
		}
		if from == target {
			return cliErr(cmd, &CliError{
				Message: "Cannot relate a worktree to itself.",
				Hint:    fmt.Sprintf("Both endpoints resolved to %s#%s.", from.Repo, from.Branch),
			})
		}

		reg := registry.New("")
		outcome, err := reg.Relate(from, target, relateType)
		if err != nil {
			return fmt.Errorf("relating: %w", err)
		}

		typ := relateType
		if typ == "" {
			typ = "related"
		}
		if relateJSON {
			return printJSON(map[string]any{
				"created": outcome == registry.RelateCreated,
				"updated": outcome == registry.RelateUpdated,
				"a":       from,
				"b":       target,
				"type":    typ,
			})
		}
		switch outcome {
		case registry.RelateCreated:
			fmt.Printf("Related %s#%s <-> %s#%s (%s)\n", from.Repo, from.Branch, target.Repo, target.Branch, typ)
		case registry.RelateUpdated:
			fmt.Printf("Updated %s#%s <-> %s#%s type to %s\n", from.Repo, from.Branch, target.Repo, target.Branch, typ)
		default:
			fmt.Printf("Already related: %s#%s <-> %s#%s (%s)\n", from.Repo, from.Branch, target.Repo, target.Branch, typ)
		}
		return nil
	},
}

var unrelateCmd = &cobra.Command{
	Use:   "unrelate <target>",
	Short: "Remove a relationship between the current worktree and another",
	Long: `Remove a durable relationship. Idempotent — unrelating a pair that
isn't related is a no-op success.

` + refFormatHelp,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		from, err := resolveRepoRef(unrelateFrom, cwd)
		if err != nil {
			return cliErr(cmd, &CliError{Message: err.Error(), Hint: refFormatHelp})
		}
		target, err := resolveRepoRef(args[0], cwd)
		if err != nil {
			return cliErr(cmd, &CliError{Message: err.Error(), Hint: refFormatHelp})
		}

		reg := registry.New("")
		removed, err := reg.Unrelate(from, target)
		if err != nil {
			return fmt.Errorf("unrelating: %w", err)
		}

		if unrelateJSON {
			return printJSON(map[string]any{
				"removed": removed,
				"a":       from,
				"b":       target,
			})
		}
		if removed {
			fmt.Printf("Unrelated %s#%s <-> %s#%s\n", from.Repo, from.Branch, target.Repo, target.Branch)
		} else {
			fmt.Printf("Not related: %s#%s <-> %s#%s (nothing to remove)\n", from.Repo, from.Branch, target.Repo, target.Branch)
		}
		return nil
	},
}

// resolveRepoRef turns a user-supplied endpoint into a durable (repo, branch)
// reference. Accepts owner/name#branch, a worktree path, or a bare branch
// resolved against the repo in cwd. An empty input defaults to the cwd worktree.
func resolveRepoRef(input, cwd string) (registry.RepoRef, error) {
	if input == "" {
		input = cwd
	}

	if repo, branch, ok := splitRepoBranch(input); ok {
		return registry.RepoRef{Repo: repo, Branch: branch}, nil
	}

	if info, err := os.Stat(input); err == nil && info.IsDir() {
		abs, _ := filepath.Abs(input)
		repo := worktree.RepoSlugFromRemote(abs)
		if repo == "" {
			return registry.RepoRef{}, fmt.Errorf("%s has no GitHub 'origin' remote to derive owner/name from", input)
		}
		branch := worktree.CurrentBranch(abs)
		if branch == "" {
			return registry.RepoRef{}, fmt.Errorf("could not determine the checked-out branch at %s", input)
		}
		return registry.RepoRef{Repo: repo, Branch: branch}, nil
	}

	// Bare branch resolved against the current repo.
	repo := worktree.RepoSlugFromRemote(cwd)
	if repo == "" {
		return registry.RepoRef{}, fmt.Errorf("no GitHub 'origin' remote in the current repo to resolve branch %q against", input)
	}
	return registry.RepoRef{Repo: repo, Branch: input}, nil
}

// splitRepoBranch parses the explicit owner/name#branch form. ok is false when
// there is no '#', or either side is empty.
func splitRepoBranch(input string) (repo, branch string, ok bool) {
	i := strings.Index(input, "#")
	if i < 0 {
		return "", "", false
	}
	repo = input[:i]
	branch = input[i+1:]
	if repo == "" || branch == "" {
		return "", "", false
	}
	return repo, branch, true
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding json: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
