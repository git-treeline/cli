package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/git-treeline/cli/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configEditCmd)
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage user-level configuration",
	Long: `View or modify the user-level Git Treeline config (port ranges, Redis
strategy, etc.). If no subcommand is given, prints all current settings.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return configListCmd.RunE(cmd, args)
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		fmt.Println(uc.Path)
		return nil
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Print all config values",
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		if !uc.Exists() {
			fmt.Println("No config file found. Run 'gtl config set <key> <value>' to create one.")
			return nil
		}
		data, err := json.MarshalIndent(uc.Data, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding config: %w", err)
		}
		fmt.Println(string(data))
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print a config value (dot notation: port.base)",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		keys := []string{
			"port.base", "port.increment", "port.reservations",
			"redis.strategy", "redis.url",
			"router.port",
			"editor.name",
			"tunnel.default",
		}
		return keys, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		val := uc.Get(args[0])
		if val == nil {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Key %q not found.", args[0]),
				Hint:    "Use 'gtl config list' to see all keys, or 'gtl config set <key> <value>' to create one.",
			})
		}
		switch v := val.(type) {
		case map[string]any:
			data, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return fmt.Errorf("encoding config value: %w", err)
			}
			fmt.Println(string(data))
		case float64:
			if v == float64(int(v)) {
				fmt.Println(int(v))
			} else {
				fmt.Println(v)
			}
		default:
			fmt.Println(v)
		}
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value (dot notation: port.base 4000)",
	Args:  cobra.ExactArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		keys := []string{
			"port.base", "port.increment", "port.reservations",
			"redis.strategy", "redis.url",
			"router.port",
			"editor.name",
			"tunnel.default",
		}
		return keys, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")

		key, raw := args[0], args[1]
		var value any
		if n, err := strconv.ParseFloat(raw, 64); err == nil {
			value = n
		} else if raw == "true" {
			value = true
		} else if raw == "false" {
			value = false
		} else {
			value = raw
		}

		uc.Set(key, value)
		if err := uc.Save(); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		fmt.Printf("%s = %v\n", key, value)
		return nil
	},
}

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open the config file in $EDITOR",
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		if !uc.Exists() {
			if err := uc.Init(); err != nil {
				return err
			}
		}

		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		c := exec.Command(editor, uc.Path)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}
