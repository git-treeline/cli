package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/confirm"
	"github.com/git-treeline/git-treeline/internal/detect"
	"github.com/git-treeline/git-treeline/internal/platform"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/git-treeline/git-treeline/internal/templates"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(installCmd)
}

var installSelect = confirm.Select
var installServeRunner = runServeInstall
var routerHealthChecker = routerInstallIssues

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Set up git-treeline for this project and machine",
	Long: `One command to get a developer productive with git-treeline.

Safe to run on first clone or any time after — every step is idempotent.

What it does:
  1. Creates .treeline.yml if missing
  2. Creates user config (if missing)
  3. Installs the post-checkout hook for automatic worktree setup
  4. Allocates ports and writes env for the current worktree
  5. Optionally enables local HTTPS routing (requires sudo)`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		absPath, err := filepath.Abs(".")
		if err != nil {
			return fmt.Errorf("could not resolve current directory: %w", err)
		}
		worktreeRoot := worktree.DetectRepoRoot(absPath)
		mainRepo := worktree.DetectMainRepo(worktreeRoot)

		configPath := filepath.Join(worktreeRoot, config.ProjectConfigFile)
		if _, err := os.Stat(configPath); err != nil {
			fmt.Println(style.Actionf("No %s found, detecting framework...", config.ProjectConfigFile))
			if err := runInitForNew(worktreeRoot, detect.Detect(worktreeRoot)); err != nil {
				return err
			}
		}

		// Step 1: user config
		uc := config.LoadUserConfig("")
		if !uc.Exists() {
			if err := uc.Init(); err != nil {
				return err
			}
			fmt.Println(style.Actionf("Created user config at %s", platform.ConfigFile()))
		} else {
			fmt.Println(style.Dimf("User config: %s", platform.ConfigFile()))
		}

		// Step 2: post-checkout hook
		hookPath, err := templates.InstallPostCheckoutHook(mainRepo)
		if err != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("Could not install post-checkout hook: %s", err))
		} else if hookPath != "" {
			fmt.Println(style.Actionf("Post-checkout hook: %s", hookPath))
		}

		// Step 3: setup (allocate ports, write env, copy files)
		if err := checkDriftOrAbort(worktreeRoot); err != nil {
			return cliErr(cmd, err)
		}

		s := setup.New(worktreeRoot, "", uc)
		if _, err := s.Run(); err != nil {
			return err
		}

		pc := config.LoadProjectConfig(worktreeRoot)
		printSetupDiagnostics(worktreeRoot, pc)

		// Step 4: HTTPS router (optional, prompted)
		if err := maybeOfferServeInstall(uc); err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(style.Actionf("Ready. Run %s to start your server.", style.Cmd("gtl start")))
		return nil
	},
}

// maybeOfferServeInstall checks if the HTTPS router is already configured.
// If not, prompts the user to install it with a link to docs.
func maybeOfferServeInstall(uc *config.UserConfig) error {
	mode := uc.RouterMode()
	if routerIsHealthy() {
		if mode == config.RouterModePrompt {
			uc.SetRouterMode(config.RouterModeEnabled)
			if err := uc.Save(); err != nil {
				fmt.Fprintln(os.Stderr, style.Warnf("Could not persist router preference: %v", err))
			}
		}
		if mode != config.RouterModeDisabled {
			fmt.Println(style.Dimf("HTTPS router: already running"))
		}
		return nil
	}

	if mode == config.RouterModeDisabled {
		return nil
	}

	if os.Getenv("GTL_HEADLESS") != "" {
		if mode == config.RouterModeEnabled {
			fmt.Fprintln(os.Stderr, style.Warnf("HTTPS router is enabled in config but not installed; skipping router setup in headless mode."))
			fmt.Fprintln(os.Stderr, style.Dimf("  Run 'gtl serve install' or re-run 'gtl install' interactively to repair it."))
		}
		return nil
	}

	if !supportsServeInstall() {
		if mode == config.RouterModeEnabled {
			return fmt.Errorf("HTTPS router is enabled in config but setup is only supported on macOS and Linux")
		}
		return nil
	}

	if mode == config.RouterModeEnabled {
		fmt.Println()
		fmt.Println(style.Actionf("Repairing HTTPS router..."))
		if err := installServeRunner(uc); err != nil {
			return err
		}
		if issues := routerHealthChecker(); len(issues) > 0 {
			return fmt.Errorf("HTTPS router install incomplete: missing %s", strings.Join(issues, ", "))
		}
		fmt.Println(style.Dimf("HTTPS router: running"))
		return nil
	}

	fmt.Println()
	fmt.Printf("Local HTTPS routing lets you access worktrees at https://{project}-{branch}.%s\n", uc.RouterDomain())
	fmt.Println("This requires sudo to trust a local CA and forward port 443.")
	fmt.Println(style.Dimf("Learn more: https://git-treeline.dev/docs/networking/#the-https-router-gtl-serve"))
	fmt.Println()

	choice := installSelect("HTTPS router setup:", []string{
		"Yes",
		"No",
		"No, and don't ask again",
	}, 1, nil)
	if choice != 0 {
		fmt.Println(style.Dimf("Skipped. Run 'gtl serve install' any time to enable."))
		if choice == 2 {
			uc.SetRouterMode(config.RouterModeDisabled)
			if err := uc.Save(); err != nil {
				fmt.Fprintln(os.Stderr, style.Warnf("Could not save router preference: %v", err))
			} else {
				fmt.Println(style.Dimf("HTTPS router setup disabled. Re-enable with: gtl config set router.mode prompt"))
			}
		}
		return nil
	}
	fmt.Println()

	uc.SetRouterMode(config.RouterModeEnabled)
	if err := uc.Save(); err != nil {
		return fmt.Errorf("saving router preference: %w", err)
	}
	if err := installServeRunner(uc); err != nil {
		return err
	}
	if issues := routerHealthChecker(); len(issues) > 0 {
		return fmt.Errorf("HTTPS router install incomplete: missing %s", strings.Join(issues, ", "))
	}
	return nil
}

// runServeInstall performs the HTTPS router installation steps.
// Extracted from serveInstallCmd so gtl install can reuse it.
func runServeInstall(uc *config.UserConfig) error {
	gtlPath, err := service.StableExecutablePath()
	if err != nil {
		return fmt.Errorf("could not resolve executable path: %w", err)
	}

	port := uc.RouterPort()

	caCertFile, err := proxy.EnsureCA()
	if err != nil {
		return fmt.Errorf("CA generation failed: %w", err)
	}

	domain := uc.RouterDomain()

	if !uc.HasExplicitRouterDomain() {
		uc.Set("router.domain", domain)
	}
	uc.SetRouterMode(config.RouterModeEnabled)
	if err := uc.Save(); err != nil {
		return fmt.Errorf("saving router settings: %w", err)
	}

	fmt.Println("System password needed for:")
	fmt.Printf("  1. Trusting the CA (browsers accept *.%s)\n", domain)
	fmt.Printf("  2. Port forwarding (443 → %d)\n", port)
	fmt.Println()

	if err := proxy.TrustCA(caCertFile); err != nil {
		fmt.Fprintln(os.Stderr, style.Warnf("CA trust failed: %v", err))
		fmt.Fprintln(os.Stderr, style.Dimf("  HTTPS will work but browsers will show a certificate warning."))
	}

	if err := service.InstallPortForward(port); err != nil {
		fmt.Fprintln(os.Stderr, style.Warnf("port forwarding skipped: %v", err))
		fmt.Fprintln(os.Stderr, style.Dimf("  URLs will require a port number: https://{branch}.%s:%d", domain, port))
		fmt.Println()
	}

	if _, err := service.Install(gtlPath, port); err != nil {
		return err
	}

	hostsRequired := domain != "localhost"
	if runtime.GOOS == "darwin" || hostsRequired {
		hostnames := routeHostnames(domain)
		if len(hostnames) > 0 {
			if err := service.SyncHosts(hostnames); err != nil {
				fmt.Fprintln(os.Stderr, style.Warnf("hosts sync failed: %v", err))
				if hostsRequired {
					fmt.Fprintln(os.Stderr, style.Dimf("  Custom TLD .%s requires /etc/hosts entries.", domain))
				} else {
					fmt.Fprintln(os.Stderr, style.Dimf("  Safari may not resolve *.localhost subdomains."))
				}
				fmt.Fprintln(os.Stderr, style.Dimf("  Run 'gtl serve hosts sync' manually."))
			}
		}
	}

	return nil
}

func supportsServeInstall() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

func routerInstallIssues() []string {
	return routerInstallIssuesWith(proxy.IsCAInstalled(), service.IsRunning())
}

func routerInstallIssuesWith(caInstalled, serviceRunning bool) []string {
	var issues []string
	if !caInstalled {
		issues = append(issues, "CA trust")
	}
	if !serviceRunning {
		issues = append(issues, "router service")
	}
	return issues
}

func routerIsHealthy() bool {
	return len(routerHealthChecker()) == 0
}
