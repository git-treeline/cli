package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/dbsource"
	"github.com/spf13/cobra"
)

var (
	dbPullForce    bool
	dbPullDryRun   bool
	dbPullDebug    bool
	dbRefreshForce bool
	dbRefreshDebug bool
)

var dbPullCmd = &cobra.Command{
	Use:   "pull <env>",
	Short: "Pull a remote database into the worktree's database",
	Long: `Resolve the remote source <env> from .treeline.yml (database.sources),
pg_dump it into the worktree's tmp/gtl-db/<env>.dump, then drop and recreate the
worktree database and restore the dump into it.

The dump is retained as a reusable sample — run 'gtl db refresh' to reset the
worktree database back to it without re-downloading. Dumps live under the
worktree's tmp/ and are removed when the worktree is torn down.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDBPull(cmd, args[0])
	},
}

var dbRefreshCmd = &cobra.Command{
	Use:   "refresh [env]",
	Short: "Reset the worktree database from a pulled sample",
	Long: `Drop the worktree database and re-import a previously pulled sample from
tmp/gtl-db/<env>.dump. No network access. With no argument, refreshes from the
most recently pulled env.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDBRefresh,
}

func runDBPull(cmd *cobra.Command, env string) error {
	info, err := resolveDB()
	if err != nil {
		return err
	}
	if info.adapterName == "sqlite" {
		return cliErr(cmd, errPullNotPostgres())
	}

	pc := config.LoadProjectConfig(info.worktreeDir)
	spec, err := buildSourceSpec(pc, env)
	if err != nil {
		return cliErr(cmd, err)
	}

	src, err := dbsource.New(spec, dbsource.DefaultDeps())
	if err != nil {
		return cliErr(cmd, classifySourceError(spec, err))
	}
	conn, err := src.Resolve()
	if err != nil {
		return cliErr(cmd, classifySourceError(spec, err))
	}

	if dbPullDryRun {
		printPullPlan(env, info.target, conn, filepath.Join(dumpDir(info.worktreeDir), env+".dump"), pc)
		return nil
	}

	dir, err := ensureDumpDir(info.worktreeDir)
	if err != nil {
		return cliErr(cmd, err)
	}
	dump := filepath.Join(dir, env+".dump")
	toc := filepath.Join(dir, env+".toc")

	prompt := fmt.Sprintf("This will OVERWRITE worktree db '%s' with data from %s.", info.target, env)
	if !confirm.Prompt(prompt, dbPullForce, nil) {
		fmt.Println("Aborted.")
		return nil
	}

	p := database.NewPuller(pc.DatabaseConnArgs())
	if dbPullDebug {
		p.Logf = debugLogf
	}

	fmt.Printf("==> Dumping %s from %s → %s\n", env, conn.Host, dump)
	if err := p.Dump(toRemoteConn(conn), dump); err != nil {
		return cliErr(cmd, classifyPullError(err, conn.Host, info.target))
	}

	exts := database.Extensions{
		Require: pc.DatabaseExtensionsRequire(),
		Strip:   pc.DatabaseExtensionsStrip(),
	}
	fmt.Printf("==> Restoring into %s\n", info.target)
	if err := p.Refresh(info.target, dump, toc, exts); err != nil {
		return cliErr(cmd, classifyPullError(err, conn.Host, info.target))
	}
	_ = os.Remove(toc)

	_ = writeManifestEntry(dir, env, manifestEntry{
		Dump:       filepath.Base(dump),
		RemoteHost: conn.Host,
		RemoteDB:   conn.DBName,
		PulledAt:   time.Now().Format(time.RFC3339),
	})
	fmt.Printf("==> Done. '%s' now holds %s data. Run 'gtl db refresh' to reset to this sample.\n", info.target, env)
	return nil
}

func runDBRefresh(cmd *cobra.Command, args []string) error {
	info, err := resolveDB()
	if err != nil {
		return err
	}
	if info.adapterName == "sqlite" {
		return cliErr(cmd, errPullNotPostgres())
	}

	pc := config.LoadProjectConfig(info.worktreeDir)
	dir := dumpDir(info.worktreeDir)

	samples := availableSamples(dir)

	// Resolve which sample to refresh from. An explicit arg wins; otherwise a
	// single sample is used directly, and several samples are offered as a
	// numbered menu (defaulting to the most recently pulled).
	env := ""
	switch {
	case len(args) > 0:
		env = args[0]
	case len(samples) == 0:
		return cliErr(cmd, &CliError{
			Message: "No pulled sample to refresh from.",
			Hint:    "Run 'gtl db pull <env>' first.",
		})
	case len(samples) == 1:
		env = samples[0]
	default:
		last := manifestLast(dir)
		if dbRefreshForce {
			// Non-interactive: fall back to the last pulled, else require an arg.
			if last == "" {
				return cliErr(cmd, &CliError{
					Message: "Multiple samples are available — specify which to refresh from.",
					Hint:    "gtl db refresh <env>  (available: " + strings.Join(samples, ", ") + ")",
				})
			}
			env = last
			break
		}
		msg := fmt.Sprintf("Refresh worktree db '%s' from which sample?", info.target)
		idx := confirm.Select(msg, samples, sampleIndex(samples, last), nil)
		env = samples[idx]
	}

	dump := filepath.Join(dir, env+".dump")
	if _, err := os.Stat(dump); err != nil {
		hint := fmt.Sprintf("Run 'gtl db pull %s' to fetch it.", env)
		if len(samples) > 0 {
			hint = "Available samples: " + strings.Join(samples, ", ") +
				fmt.Sprintf(". Or run 'gtl db pull %s' to fetch it.", env)
		}
		return cliErr(cmd, &CliError{
			Message: fmt.Sprintf("No retained sample for '%s'.", env),
			Hint:    hint,
		})
	}

	// Always confirm the destructive overwrite. A menu selection is not a
	// substitute: confirm.Select returns its default on EOF/invalid input,
	// whereas confirm.Prompt safely returns false, so a non-interactive run
	// can never silently overwrite the worktree db.
	prompt := fmt.Sprintf("This will OVERWRITE worktree db '%s' from the local %s sample.", info.target, env)
	if !confirm.Prompt(prompt, dbRefreshForce, nil) {
		fmt.Println("Aborted.")
		return nil
	}

	toc := filepath.Join(dir, env+".toc")
	p := database.NewPuller(pc.DatabaseConnArgs())
	if dbRefreshDebug {
		p.Logf = debugLogf
	}
	exts := database.Extensions{
		Require: pc.DatabaseExtensionsRequire(),
		Strip:   pc.DatabaseExtensionsStrip(),
	}

	fmt.Printf("==> Refreshing %s from %s sample\n", info.target, env)
	if err := p.Refresh(info.target, dump, toc, exts); err != nil {
		return cliErr(cmd, classifyPullError(err, "", info.target))
	}
	_ = os.Remove(toc)
	fmt.Printf("==> Done. '%s' reset to %s sample.\n", info.target, env)
	return nil
}

// --- helpers ---

func buildSourceSpec(pc *config.ProjectConfig, env string) (dbsource.Spec, error) {
	cfg, ok := pc.DatabaseSourceSpec(env)
	if !ok {
		return dbsource.Spec{}, errUnknownSourceEnv(env, pc.DatabaseSourceEnvs())
	}
	return dbsource.Spec{
		Env:     env,
		Via:     cfg.Via,
		App:     cfg.App,
		Var:     cfg.Var,
		URLEnv:  cfg.URLEnv,
		SSLMode: pc.DatabaseSSLMode(),
	}, nil
}

func toRemoteConn(c *dbsource.ConnInfo) *database.RemoteConn {
	return &database.RemoteConn{
		Host:     c.Host,
		Port:     c.Port,
		User:     c.User,
		Password: c.Password,
		DBName:   c.DBName,
		SSLMode:  c.SSLMode,
	}
}

func dumpDir(worktreeDir string) string {
	return filepath.Join(worktreeDir, "tmp", "gtl-db")
}

// ensureDumpDir creates the worktree's dump directory and a self-ignoring
// .gitignore so large dumps can't be accidentally staged.
func ensureDumpDir(worktreeDir string) (string, error) {
	dir := dumpDir(worktreeDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(gi, []byte("*\n"), 0o644)
	}
	return dir, nil
}

func printPullPlan(env, target string, conn *dbsource.ConnInfo, dump string, pc *config.ProjectConfig) {
	fmt.Printf("Plan for 'gtl db pull %s' (dry run):\n", env)
	fmt.Printf("  remote:      %s@%s:%s/%s (sslmode=%s, password=***)\n",
		conn.User, conn.Host, conn.Port, conn.DBName, conn.SSLMode)
	fmt.Printf("  dump file:   %s\n", dump)
	fmt.Printf("  restore to:  %s (worktree db)\n", target)
	if req := pc.DatabaseExtensionsRequire(); len(req) > 0 {
		fmt.Printf("  require ext: %s\n", strings.Join(req, ", "))
	}
	if strip := pc.DatabaseExtensionsStrip(); len(strip) > 0 {
		fmt.Printf("  strip ext:   %s\n", strings.Join(strip, ", "))
	}
	fmt.Println("  (no changes made)")
}

func debugLogf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "  [debug] "+format+"\n", a...)
}

func errPullNotPostgres() error {
	return &CliError{
		Message: "gtl db pull/refresh supports the postgresql adapter only.",
		Hint:    "This worktree's database.adapter is not postgresql.",
	}
}

// classifySourceError maps a dbsource resolution error to a user-facing CliError.
func classifySourceError(spec dbsource.Spec, err error) error {
	switch {
	case errors.Is(err, dbsource.ErrFlyNotInstalled):
		return errFlyNotInstalled()
	case errors.Is(err, dbsource.ErrFlyNotAuthed):
		return errFlyNotAuthed(spec.App)
	}
	var ve *dbsource.VarNotFoundError
	if errors.As(err, &ve) {
		return errSourceVarNotFound(ve.Env, ve.Var)
	}
	var ue *dbsource.UnknownViaError
	if errors.As(err, &ue) {
		return errUnknownVia(ue.Env, ue.Via)
	}
	return err
}

// classifyPullError maps a failed pg tool call to a user-facing CliError based
// on its stage and captured stderr.
func classifyPullError(err error, host, target string) error {
	var ee *database.ExecError
	if !errors.As(err, &ee) {
		return err
	}
	// Some failures (notably a missing binary) carry no stderr — the detail is
	// only in Err — so classify against both.
	detail := ee.Output
	if ee.Err != nil {
		detail = strings.TrimSpace(detail + "\n" + ee.Err.Error())
	}
	return classifyExec(ee.Stage, detail, host, target, err)
}

func classifyExec(stage, detail, host, target string, fallback error) error {
	low := strings.ToLower(detail)
	switch {
	case strings.Contains(low, "command not found") || strings.Contains(low, "executable file not found"):
		return errPgToolMissing(pgToolForStage(stage))
	case stage == "dump" && dialFailed(low):
		return errRemoteDialTimeout(host)
	case versionSkew(low):
		return errVersionSkew(detail)
	case missingExtension(low):
		return errMissingExtension(detail)
	case strings.Contains(low, "being accessed by other users"):
		return errDropBlocked(target)
	case stage == "validate":
		return errCorruptDump()
	}
	return fallback
}

func dialFailed(low string) bool {
	return strings.Contains(low, "timeout") ||
		strings.Contains(low, "timed out") ||
		strings.Contains(low, "could not connect") ||
		strings.Contains(low, "connection refused") ||
		strings.Contains(low, "no route to host") ||
		strings.Contains(low, "could not translate host name")
}

func versionSkew(low string) bool {
	return strings.Contains(low, "server version mismatch") ||
		strings.Contains(low, "aborting because of server version") ||
		(strings.Contains(low, "server version") && strings.Contains(low, "version mismatch"))
}

func missingExtension(low string) bool {
	return strings.Contains(low, "could not open extension control file") ||
		(strings.Contains(low, "extension") && strings.Contains(low, "does not exist"))
}

func pgToolForStage(stage string) string {
	switch stage {
	case "dump":
		return "pg_dump"
	case "validate", "restore":
		return "pg_restore"
	default:
		return "psql"
	}
}

// --- dump manifest (tmp/gtl-db/manifest.json) ---

type dumpManifest struct {
	Last string                   `json:"last"`
	Envs map[string]manifestEntry `json:"envs"`
}

type manifestEntry struct {
	Dump       string `json:"dump"`
	RemoteHost string `json:"remote_host"`
	RemoteDB   string `json:"remote_db"`
	PulledAt   string `json:"pulled_at"`
}

// availableSamples lists the env names that have a retained dump file on disk,
// sorted. It scans the directory directly (not the manifest) so it stays
// accurate even if the manifest is missing or stale.
func availableSamples(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.dump"))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, strings.TrimSuffix(filepath.Base(m), ".dump"))
	}
	sort.Strings(out)
	return out
}

// sampleIndex returns the position of env in samples, or 0 (the first option)
// when env is empty or not present — used as the default menu selection.
func sampleIndex(samples []string, env string) int {
	for i, s := range samples {
		if s == env {
			return i
		}
	}
	return 0
}

func manifestPath(dir string) string { return filepath.Join(dir, "manifest.json") }

func readManifest(dir string) dumpManifest {
	m := dumpManifest{Envs: map[string]manifestEntry{}}
	raw, err := os.ReadFile(manifestPath(dir))
	if err != nil {
		return m
	}
	_ = json.Unmarshal(raw, &m)
	if m.Envs == nil {
		m.Envs = map[string]manifestEntry{}
	}
	return m
}

func writeManifestEntry(dir, env string, entry manifestEntry) error {
	m := readManifest(dir)
	m.Last = env
	m.Envs[env] = entry
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath(dir), raw, 0o644)
}

func manifestLast(dir string) string { return readManifest(dir).Last }
