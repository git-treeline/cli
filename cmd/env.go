package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/spf13/cobra"
)

var envJSON bool
var envTemplate bool

var envLineRE = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

func init() {
	envCmd.Flags().BoolVar(&envJSON, "json", false, "Output as JSON")
	envCmd.Flags().BoolVar(&envTemplate, "template", false, "Print unresolved env: template from .treeline.yml")
	rootCmd.AddCommand(envCmd)
}

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Show env file contents and Treeline-managed keys for this worktree",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)
		// Load from worktree (not mainRepo) so branch-specific config is respected
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
			return errNoAllocation(absPath)
		}

		envPath := filepath.Join(absPath, pc.EnvFileTarget())
		if _, err := os.Stat(envPath); err != nil {
			if os.IsNotExist(err) {
				return &CliError{
					Message: fmt.Sprintf("Env file does not exist: %s", envPath),
					Hint:    "Run 'gtl setup' to generate it from .treeline.yml env: template.",
				}
			}
			return err
		}

		entries, err := parseEnvLines(envPath)
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
			varsMap[e.key] = e.val
		}

		if envJSON {
			managedKeys := make([]string, 0, len(managed))
			for k := range managed {
				managedKeys = append(managedKeys, k)
			}
			sort.Strings(managedKeys)
			data, err := json.MarshalIndent(map[string]any{
				"file":              pc.EnvFileTarget(),
				"vars":              varsMap,
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
			d := fmt.Sprintf("%s=%s", e.key, strconv.Quote(e.val))
			_, tl := managed[e.key]
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

type envEntry struct {
	key string
	val string
}

func stripEnvQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			if u, err := strconv.Unquote(s); err == nil {
				return u
			}
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return strings.ReplaceAll(s[1:len(s)-1], `\'`, `'`)
		}
	}
	return s
}

func parseEnvLines(path string) ([]envEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []envEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := envLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entries = append(entries, envEntry{key: m[1], val: stripEnvQuotes(m[2])})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
