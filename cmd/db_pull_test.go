package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
)

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if manifestLast(dir) != "" {
		t.Error("empty dir should have no last env")
	}
	if err := writeManifestEntry(dir, "production", manifestEntry{Dump: "production.dump", RemoteHost: "h"}); err != nil {
		t.Fatal(err)
	}
	if got := manifestLast(dir); got != "production" {
		t.Errorf("last = %q, want production", got)
	}
	if err := writeManifestEntry(dir, "staging", manifestEntry{Dump: "staging.dump"}); err != nil {
		t.Fatal(err)
	}
	if got := manifestLast(dir); got != "staging" {
		t.Errorf("last = %q, want staging (most recent)", got)
	}
	m := readManifest(dir)
	if len(m.Envs) != 2 {
		t.Errorf("expected 2 env entries, got %d", len(m.Envs))
	}
	if m.Envs["production"].Dump != "production.dump" {
		t.Errorf("production entry not retained: %+v", m.Envs["production"])
	}
}

func TestEnsureDumpDir(t *testing.T) {
	wt := t.TempDir()
	dir, err := ensureDumpDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(wt, "tmp", "gtl-db") {
		t.Errorf("dir = %q", dir)
	}
	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("expected .gitignore: %v", err)
	}
	if strings.TrimSpace(string(gi)) != "*" {
		t.Errorf(".gitignore = %q, want *", gi)
	}
	// idempotent
	if _, err := ensureDumpDir(wt); err != nil {
		t.Fatalf("second call failed: %v", err)
	}
}

func TestClassifyExec(t *testing.T) {
	cases := []struct {
		name   string
		stage  string
		output string
		want   string // substring expected in the CliError message
	}{
		{"dial timeout", "dump", "could not connect to server: Connection timed out", "remote database host"},
		{"connection refused", "dump", "connection refused", "remote database host"},
		{"version skew", "dump", "aborting because of server version mismatch", "version mismatch"},
		{"missing extension", "restore", `could not open extension control file "pgaudit"`, "extension"},
		{"drop blocked", "drop", "database \"club_feat\" is being accessed by other users", "active connections"},
		{"tool missing", "dump", "exec: \"pg_dump\": executable file not found in $PATH", "not found on PATH"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := classifyExec(c.stage, c.output, "db.example.com", "club_feat", nil)
			ce, ok := err.(*CliError)
			if !ok {
				t.Fatalf("want *CliError, got %T: %v", err, err)
			}
			combined := ce.Message + " " + ce.Hint
			if !strings.Contains(strings.ToLower(combined), strings.ToLower(c.want)) {
				t.Errorf("message %q + hint %q missing %q", ce.Message, ce.Hint, c.want)
			}
		})
	}
}

func TestClassifyExec_Unmatched(t *testing.T) {
	fallback := &CliError{Message: "orig"}
	if got := classifyExec("restore", "some weird error", "", "", fallback); got != error(fallback) {
		t.Errorf("unmatched stderr should return fallback, got %v", got)
	}
}

func TestClassifyPullError_NonExec(t *testing.T) {
	plain := &CliError{Message: "plain"}
	if got := classifyPullError(plain, "", ""); got != error(plain) {
		t.Errorf("non-ExecError should pass through, got %v", got)
	}
	ee := &database.ExecError{Stage: "dump", Output: "connection refused"}
	got := classifyPullError(ee, "h", "t")
	ce, ok := got.(*CliError)
	if !ok || !strings.Contains(ce.Message, "remote database host") {
		t.Errorf("ExecError not classified: %v", got)
	}
}

func TestClassifyPullError_ToolMissingFromErr(t *testing.T) {
	// Real exec.Command failure for a missing binary carries no stderr — the
	// detail is only in Err. It must still map to the friendly missing-tool error.
	ee := &database.ExecError{
		Stage:  "dump",
		Output: "",
		Err:    errors.New(`exec: "pg_dump": executable file not found in $PATH`),
	}
	got := classifyPullError(ee, "h", "t")
	ce, ok := got.(*CliError)
	if !ok || !strings.Contains(ce.Message, "pg_dump not found") {
		t.Errorf("missing-tool from Err not classified: %v", got)
	}
}

func TestClassifyPullError_CorruptDump(t *testing.T) {
	ee := &database.ExecError{Stage: "validate", Output: "pg_restore: error: did not find magic string"}
	got := classifyPullError(ee, "", "club_feat")
	ce, ok := got.(*CliError)
	if !ok || !strings.Contains(strings.ToLower(ce.Message), "corrupt") {
		t.Errorf("corrupt dump not classified: %v", got)
	}
}

// --- integration: dry-run resolves and prints without side effects ---

func writeRegistry(t *testing.T, worktree string) {
	t.Helper()
	reg := registry.New("")
	if err := os.MkdirAll(filepath.Dir(reg.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	json := `{"version":1,"allocations":[{"worktree":"` + worktree +
		`","database":"club_feat","project":"club","database_adapter":"postgresql"}]}`
	if err := os.WriteFile(reg.Path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDBPull_DryRun_NoSideEffects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GTL_HOME", home)
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".treeline.yml"), []byte(`
project: club
database:
  adapter: postgresql
  template: club_development
  sources:
    staging:
      via: env
      var: STAGING_DATABASE_URL
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, wt)
	t.Setenv("STAGING_DATABASE_URL", "postgres://u:p@db.example.com:5432/club_staging")
	t.Chdir(wt)

	dbPullDryRun = true
	t.Cleanup(func() { dbPullDryRun = false })

	out := captureStdout(t, func() {
		if err := runDBPull(dbPullCmd, "staging"); err != nil {
			t.Fatalf("dry-run failed: %v", err)
		}
	})

	if !strings.Contains(out, "db.example.com") || !strings.Contains(out, "club_staging") {
		t.Errorf("plan missing remote details:\n%s", out)
	}
	if strings.Contains(out, ":p@") || strings.Contains(out, "password=p") {
		t.Errorf("password leaked into plan:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(wt, "tmp", "gtl-db")); err == nil {
		t.Error("dry-run must not create tmp/gtl-db")
	}
}

func TestDBPull_UnknownEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GTL_HOME", home)
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".treeline.yml"), []byte(`
project: club
database:
  adapter: postgresql
  template: club_development
  sources:
    staging:
      via: env
      var: STAGING_DATABASE_URL
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, wt)
	t.Chdir(wt)

	err := runDBPull(dbPullCmd, "production")
	ce, ok := err.(*CliError)
	if !ok {
		t.Fatalf("want *CliError, got %T: %v", err, err)
	}
	if !strings.Contains(ce.Hint, "staging") {
		t.Errorf("hint should list available envs: %q", ce.Hint)
	}
}

func TestAvailableSamples(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"production.dump", "staging.dump", "manifest.json", "production.toc"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := availableSamples(dir)
	want := []string{"production", "staging"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("samples = %v, want %v (only *.dump, sorted)", got, want)
	}
}

// refreshFixture sets up a worktree with a registry and an empty .treeline.yml
// (postgresql adapter), returning the worktree dir and its dump dir.
func refreshFixture(t *testing.T) (wt, dir string) {
	t.Helper()
	t.Setenv("GTL_HOME", t.TempDir())
	wt = t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".treeline.yml"), []byte("project: club\ndatabase:\n  adapter: postgresql\n  template: club_development\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, wt)
	t.Chdir(wt)
	dir, err := ensureDumpDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	return wt, dir
}

func TestDBRefresh_NoSample(t *testing.T) {
	refreshFixture(t)
	err := runDBRefresh(dbRefreshCmd, nil)
	ce, ok := err.(*CliError)
	if !ok {
		t.Fatalf("want *CliError, got %T: %v", err, err)
	}
	if !strings.Contains(ce.Hint, "gtl db pull") {
		t.Errorf("hint should point to pull: %q", ce.Hint)
	}
}

func TestDBRefresh_MultipleSamplesForceAmbiguous(t *testing.T) {
	_, dir := refreshFixture(t)
	for _, env := range []string{"production", "staging"} {
		if err := os.WriteFile(filepath.Join(dir, env+".dump"), []byte("PGDMP"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// --force is non-interactive, so an ambiguous set with no manifest pointer
	// can't be resolved and must error listing the options.
	dbRefreshForce = true
	t.Cleanup(func() { dbRefreshForce = false })

	err := runDBRefresh(dbRefreshCmd, nil)
	ce, ok := err.(*CliError)
	if !ok {
		t.Fatalf("want *CliError, got %T: %v", err, err)
	}
	if !strings.Contains(ce.Message, "Multiple samples") ||
		!strings.Contains(ce.Hint, "production") || !strings.Contains(ce.Hint, "staging") {
		t.Errorf("expected multi-sample error listing both: %q / %q", ce.Message, ce.Hint)
	}
}

// TestDBRefresh_MultipleSamplesEOFAborts guards the original confirmation-bypass
// bug: with several samples and no --force, an interactive menu selection on an
// EOF stdin returns the default choice, but the follow-up overwrite prompt must
// still abort (confirm.Prompt returns false on EOF) — never drop/restore.
func TestDBRefresh_MultipleSamplesEOFAborts(t *testing.T) {
	_, dir := refreshFixture(t)
	for _, env := range []string{"production", "staging"} {
		if err := os.WriteFile(filepath.Join(dir, env+".dump"), []byte("PGDMP"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// stdin at EOF: the pipe's write end is closed, so reads return EOF at once.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })

	var runErr error
	out := captureStdout(t, func() { runErr = runDBRefresh(dbRefreshCmd, nil) })

	// A clean abort returns nil and prints "Aborted." If the bug regressed,
	// the command would skip the prompt and proceed to drop/pg_restore, which
	// (no Postgres reachable here) would surface as a non-nil error instead.
	if runErr != nil {
		t.Fatalf("expected clean abort on EOF, got error (did it try to drop/restore?): %v", runErr)
	}
	if !strings.Contains(out, "Aborted.") {
		t.Errorf("expected abort on EOF stdin, got output:\n%s", out)
	}
}

func TestSampleIndex(t *testing.T) {
	samples := []string{"production", "staging"}
	if got := sampleIndex(samples, "staging"); got != 1 {
		t.Errorf("sampleIndex(staging) = %d, want 1", got)
	}
	if got := sampleIndex(samples, "missing"); got != 0 {
		t.Errorf("sampleIndex(missing) = %d, want 0 (default first)", got)
	}
	if got := sampleIndex(samples, ""); got != 0 {
		t.Errorf("sampleIndex(empty) = %d, want 0", got)
	}
}

func TestDBRefresh_UnknownEnvListsAvailable(t *testing.T) {
	_, dir := refreshFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "production.dump"), []byte("PGDMP"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runDBRefresh(dbRefreshCmd, []string{"staging"})
	ce, ok := err.(*CliError)
	if !ok {
		t.Fatalf("want *CliError, got %T: %v", err, err)
	}
	if !strings.Contains(ce.Hint, "production") {
		t.Errorf("hint should list available sample 'production': %q", ce.Hint)
	}
}
