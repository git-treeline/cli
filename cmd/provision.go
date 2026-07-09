package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/dbsource"
	"github.com/git-treeline/cli/internal/provision"
	"github.com/git-treeline/cli/internal/style"
	"github.com/spf13/cobra"
)

var provisionDryRun bool

func init() {
	provisionCmd.Flags().BoolVar(&provisionDryRun, "dry-run", false, "Print the provisioning plan without making changes")
	rootCmd.AddCommand(provisionCmd)
}

var provisionCmd = &cobra.Command{
	Use:   "provision [PATH]",
	Short: "Make a host meet a repo's declared prerequisites for setup",
	Long: `Bring the current host up to the baseline a repo needs before 'gtl setup'
can succeed, from the provision: section of .treeline.yml:

  provision:
    apt: [libvips, imagemagick]      # system packages (Linux; needs sudo)
    services: [redis-server]         # apt-install + systemctl enable --now
    database:
      source: production             # hydrate the template from real data
      hydrate: "bin/rails db:schema:load db:seed"   # fallback command

Every step checks before acting, so provision is safe to re-run. apt and
services are Linux-only and are skipped with a note on macOS. The template
database is the same one gtl clones worktree databases from (database.template);
provision creates it from a configured source, or via the hydrate command, or
empty as a last resort.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		abs, _ := filepath.Abs(path)
		pc := config.LoadProjectConfig(abs)

		if !pc.Exists() {
			fmt.Println(style.Actionf("Nothing to provision — no %s here.", config.ProjectConfigFile))
			return nil
		}
		cfg := pc.Provision()
		if !cfg.Present {
			fmt.Println(style.Actionf("Nothing to provision — no 'provision:' section in %s.", config.ProjectConfigFile))
			return nil
		}

		actions := provision.BuildPlan(cfg, abs, runtime.GOOS, fileExists)
		actions = guardDatabaseAdapter(actions, pc)

		if provisionDryRun {
			provision.PrintPlan(os.Stdout, actions)
			return nil
		}

		if len(actions) == 0 {
			fmt.Println(style.Actionf("Nothing to provision."))
			return nil
		}

		deps := provisionDeps(pc, abs)
		if err := provision.Run(actions, abs, deps); err != nil {
			return cliErr(cmd, err)
		}
		fmt.Println()
		fmt.Println(style.Successf("Provisioning complete."))
		return nil
	},
}

// guardDatabaseAdapter drops the database action when the adapter isn't
// postgresql — provision's database step drives createdb/psql/pg tooling.
func guardDatabaseAdapter(actions []provision.Action, pc *config.ProjectConfig) []provision.Action {
	if pc.DatabaseAdapter() == "postgresql" {
		return actions
	}
	var out []provision.Action
	for _, a := range actions {
		if a.Kind == provision.ActionDatabase {
			fmt.Fprintln(os.Stderr, style.Warnf("Skipping database provisioning — adapter %q is not postgresql.", pc.DatabaseAdapter()))
			continue
		}
		out = append(out, a)
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// provisionDeps wires the executor's seams to real system effects.
func provisionDeps(pc *config.ProjectConfig, repoDir string) provision.Deps {
	connArgs := pc.DatabaseConnArgs()
	adapter, _ := database.ForAdapter(pc.DatabaseAdapter(), connArgs)
	return provision.Deps{
		GOOS:             runtime.GOOS,
		FileExists:       fileExists,
		LookPath:         exec.LookPath,
		PackageInstalled: dpkgInstalled,
		AptInstall:       aptInstall,
		ServiceEnable:    systemctlEnableNow,
		RunInDir:         runShellInDir,
		DBExists: func(name string) (bool, error) {
			if adapter == nil {
				return false, fmt.Errorf("no database adapter")
			}
			return adapter.Exists(name)
		},
		CreateDB: func(name string) error { return createDB(connArgs, name) },
		HydrateFromSource: func(template, env string) error {
			return hydrateTemplateFromSource(pc, template, env)
		},
		Log: func(format string, a ...any) {
			fmt.Println(style.Actionf(format, a...))
		},
		Warn: func(format string, a ...any) {
			fmt.Fprintln(os.Stderr, style.Warnf(format, a...))
		},
	}
}

// dpkgInstalled reports whether an apt package is installed. An unknown package
// makes dpkg-query exit non-zero, which we treat as "not installed".
func dpkgInstalled(pkg string) (bool, error) {
	out, err := exec.Command("dpkg-query", "-W", "-f=${Status}", pkg).Output()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), "install ok installed"), nil
}

func aptInstall(pkgs []string) error {
	args := append([]string{"apt-get", "install", "-y"}, pkgs...)
	return streamCommand("sudo", args...)
}

func systemctlEnableNow(name string) error {
	return streamCommand("sudo", "systemctl", "enable", "--now", name)
}

func createDB(connArgs []string, name string) error {
	args := append(append([]string{}, connArgs...), name)
	return streamCommand("createdb", args...)
}

func runShellInDir(dir, command string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func streamCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// hydrateTemplateFromSource dumps a configured source env and restores it into
// the template database, creating it. Reuses the gtl db pull machinery
// (dbsource resolution + Puller) so provision and pull share one path.
func hydrateTemplateFromSource(pc *config.ProjectConfig, template, env string) error {
	spec, err := buildSourceSpec(pc, env)
	if err != nil {
		return err
	}
	src, err := dbsource.New(spec, dbsource.DefaultDeps())
	if err != nil {
		return classifySourceError(spec, err)
	}
	conn, err := src.Resolve()
	if err != nil {
		return classifySourceError(spec, err)
	}

	dir, err := os.MkdirTemp("", "gtl-provision-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	dump := filepath.Join(dir, env+".dump")
	toc := filepath.Join(dir, env+".toc")

	p := database.NewPuller(pc.DatabaseConnArgs())
	fmt.Printf("==> Dumping %s from %s\n", env, conn.Host)
	if err := p.Dump(toRemoteConn(conn), dump); err != nil {
		return classifyPullError(err, conn.Host, template)
	}
	exts := database.Extensions{
		Require: pc.DatabaseExtensionsRequire(),
		Strip:   pc.DatabaseExtensionsStrip(),
	}
	fmt.Printf("==> Restoring into template %s\n", template)
	if err := p.Refresh(template, dump, toc, exts); err != nil {
		return classifyPullError(err, conn.Host, template)
	}
	return nil
}
