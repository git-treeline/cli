package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/envparse"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/spf13/cobra"
)

var envJSON bool
var envTemplate bool

func init() {
	envCmd.PersistentFlags().BoolVar(&envJSON, "json", false, "Output as JSON")
	envCmd.PersistentFlags().BoolVar(&envTemplate, "template", false, "Print unresolved env: template from .treeline.yml")
	envCmd.AddCommand(envShowCmd)
	envCmd.AddCommand(envSyncCmd)
	rootCmd.AddCommand(envCmd)
}

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage environment variables for this worktree",
	Long: `Show or sync environment variables managed by Treeline.

Without a subcommand, shows the current env file contents (same as 'gtl env show').`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return envShowCmd.RunE(envShowCmd, args)
	},
}

var envShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show env file contents and Treeline-managed keys for this worktree",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)
		pc := config.LoadProjectConfig(absPath)

		if envTemplate {
			tmpl := pc.EnvTemplate()
			if tmpl == nil {
				return nil
			}
			keys := make([]string, 0, len(tmpl))
			for k := range tmpl {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("%s=%s\n", k, tmpl[k])
			}
			return nil
		}

		reg := registry.New("")
		entry := reg.Find(absPath)
		if entry == nil {
			return cliErr(cmd, errNoAllocation(absPath))
		}

		envPath := filepath.Join(absPath, pc.EnvFileTarget())
		if _, err := os.Stat(envPath); err != nil {
			if os.IsNotExist(err) {
				return cliErr(cmd, &CliError{
					Message: fmt.Sprintf("Env file does not exist: %s", envPath),
					Hint:    "Run 'gtl setup' to generate it from .treeline.yml env: template.",
				})
			}
			return err
		}

		entries, err := envparse.ParseFile(envPath)
		if err != nil {
			return err
		}

		tmpl := pc.EnvTemplate()
		managed := make(map[string]struct{})
		for k := range tmpl {
			managed[k] = struct{}{}
		}

		varsMap := make(map[string]string, len(entries))
		for _, e := range entries {
			varsMap[e.Key] = e.Val
		}

		if envJSON {
			managedKeys := make([]string, 0, len(managed))
			for k := range managed {
				managedKeys = append(managedKeys, k)
			}
			sort.Strings(managedKeys)
			data, err := json.MarshalIndent(map[string]any{
				"file":             pc.EnvFileTarget(),
				"vars":             varsMap,
				"treeline_managed": managedKeys,
			}, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		}

		type lineOut struct {
			display string
			tl      bool
		}
		lines := make([]lineOut, 0, len(entries))
		maxW := 0
		for _, e := range entries {
			d := fmt.Sprintf("%s=%s", e.Key, strconv.Quote(e.Val))
			_, tl := managed[e.Key]
			lines = append(lines, lineOut{display: d, tl: tl})
			if len(d) > maxW {
				maxW = len(d)
			}
		}
		for _, ln := range lines {
			if ln.tl {
				pad := maxW - len(ln.display)
				if pad < 0 {
					pad = 0
				}
				fmt.Printf("%s%s  [treeline]\n", ln.display, strings.Repeat(" ", pad))
			} else {
				fmt.Println(ln.display)
			}
		}
		return nil
	},
}

