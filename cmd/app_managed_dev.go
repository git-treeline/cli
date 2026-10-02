package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/git-treeline/cli/internal/devresources"
	"github.com/spf13/cobra"
)

func init() {
	command := &cobra.Command{
		Use:    "dev-resources",
		Short:  "Inspect the private Mac development resource contract without changes",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			if !appManagedDev() || !jsonOutput {
				return fmt.Errorf("dev-resources requires app-managed dev and --json")
			}
			if _, err := devresources.Check(); err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
				"ready": true, "contract": "mac-dev-port-pool-v1",
			})
		},
	}
	command.Flags().Bool("json", false, "Return the resource admission result as JSON")
	rootCmd.AddCommand(command)
}

func appManagedDev() bool {
	return os.Getenv("TREELINE_MANAGED") == "app" && os.Getenv("TREELINE_CHANNEL") == "dev"
}

// App development hosts may inspect their private state, but provisioning is
// withheld until ports, databases and services have independent ownership.
func guardAppManagedDev(cmd *cobra.Command, _ []string) error {
	if !appManagedDev() {
		return nil
	}
	if err := appManagedDevHome(); err != nil {
		return err
	}
	parent := ""
	if cmd.Parent() != nil {
		parent = cmd.Parent().Name()
	}
	flag := func(name string) bool { value, _ := cmd.Flags().GetBool(name); return value }
	text := func(name string) string { value, _ := cmd.Flags().GetString(name); return value }
	root := cmd.Parent() != nil && cmd.Parent().Parent() == nil
	allowed := cmd.Name() == "help" || root && cmd.Name() == "version"
	if root {
		switch cmd.Name() {
		case "dev-resources":
			allowed = flag("json")
		case "status":
			allowed = flag("json") && !flag("watch")
		case "related":
			allowed = flag("json")
		case "env", "routes", "doctor":
			allowed = flag("json") && flag("read-only") && !flag("fix") && !flag("template")
		case "claim":
			allowed = flag("inspect-json") && text("request-file") == "" && publicReleaseUUID(text("operation-id"))
		}
	}
	if parent == "env" && cmd.Name() == "show" {
		allowed = flag("json") && flag("read-only") && !flag("template")
	}
	if parent == "release" && cmd.Name() == "status" {
		allowed = true
	}
	if !allowed {
		return fmt.Errorf("app-managed dev resources are not configured: this command is unavailable; read-only inspection remains available")
	}
	return nil
}

func appManagedDevHome() error {
	refuse := func() error {
		return fmt.Errorf("app-managed dev requires a canonical private GTL_HOME at ~/.treeline-dev/gtl")
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return refuse()
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return refuse()
	}
	expected := filepath.Join(home, ".treeline-dev", "gtl")
	if os.Getenv("GTL_HOME") != expected {
		return refuse()
	}
	for _, directory := range []string{filepath.Dir(expected), expected} {
		info, err := os.Lstat(directory)
		if os.IsNotExist(err) {
			continue
		}
		forbiddenMode := os.FileMode(0o022)
		if directory == expected {
			forbiddenMode = 0o077
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&forbiddenMode != 0 {
			return refuse()
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Getuid()) {
			return refuse()
		}
	}
	for _, name := range []string{"registry.json", "config.json"} {
		info, err := os.Lstat(filepath.Join(expected, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return refuse()
		}
	}
	return nil
}
