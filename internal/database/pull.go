package database

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// extNameRe validates a PostgreSQL extension name before it is interpolated
// into a CREATE EXTENSION statement.
var extNameRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// RemoteConn is a resolved remote PostgreSQL connection. It mirrors
// dbsource.ConnInfo; the cmd layer maps between them so this package stays a
// leaf (no dbsource import).
type RemoteConn struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// Extensions captures the extension handling for a restore: Require names are
// pre-created (CREATE EXTENSION IF NOT EXISTS), Strip names are commented out
// of the restore TOC (for cloud-only extensions absent locally).
type Extensions struct {
	Require []string
	Strip   []string
}

// Puller runs the remote-pull / refresh pipeline against a LOCAL target db.
// It reuses the same exec-seam pattern as PostgreSQL but adds an env-aware
// variant so PGPASSWORD/PGSSLMODE can be passed to the remote pg_dump via the
// process environment — never on argv.
type Puller struct {
	// LocalConnArgs are prepended to local pg tool calls (same as PostgreSQL.ConnArgs).
	LocalConnArgs []string
	// Logf, when set, receives a redacted line for each exec (for --debug).
	Logf func(format string, a ...any)

	// exec seams; nil falls through to real exec.
	execRun    func(name string, args ...string) error
	execRunEnv func(env []string, name string, args ...string) error
	execOutput func(name string, args ...string) ([]byte, error)
}

// NewPuller returns a Puller wired to real exec, prepending localConnArgs to
// local pg tool calls.
func NewPuller(localConnArgs []string) *Puller {
	return &Puller{LocalConnArgs: localConnArgs}
}

// ExecError carries the stage and captured stderr of a failed pg tool call so
// the cmd layer can classify it into a user-facing hint.
type ExecError struct {
	Stage  string // "dump", "drop", "create", "extension", "list", "restore"
	Output string // captured combined stderr (best effort)
	Err    error
}

func (e *ExecError) Error() string {
	if e.Output != "" {
		return fmt.Sprintf("%s failed: %s", e.Stage, e.Output)
	}
	return fmt.Sprintf("%s failed: %v", e.Stage, e.Err)
}

func (e *ExecError) Unwrap() error { return e.Err }

// Dump writes a custom-format dump of the remote database to dumpPath. The
// remote password and sslmode travel via the environment, so they never appear
// in argv. The dump file must exist and be non-empty before any caller drops
// the local db.
func (p *Puller) Dump(remote *RemoteConn, dumpPath string) error {
	// Dump to a temporary path and only rename into place once the dump is
	// complete and verified. A partial or interrupted dump is therefore never
	// retained as a sample — which a later refresh would restore over the
	// local db, destroying it.
	tmp := dumpPath + ".partial"
	_ = os.Remove(tmp)
	args := []string{
		"-Fc",
		"-h", remote.Host,
		"-p", remote.Port,
		"-U", remote.User,
		"-d", remote.DBName,
		"-f", tmp,
	}
	if err := p.runStage("dump", remoteEnv(remote), "pg_dump", args...); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if info, err := os.Stat(tmp); err != nil || info.Size() == 0 {
		_ = os.Remove(tmp)
		return &ExecError{Stage: "dump", Output: "pg_dump produced no output file"}
	}
	// Verify the archive is readable before committing it as the sample.
	if _, err := p.listDump(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dumpPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("finalizing dump %s: %w", dumpPath, err)
	}
	return nil
}

// Refresh drops and recreates the local target, pre-creates required
// extensions, builds a stripped restore list when needed, and restores the
// dump. It performs no network access — pull calls Dump first, refresh reuses
// an already-retained dump. listPath is where a stripped TOC may be written.
func (p *Puller) Refresh(target, dumpPath, listPath string, exts Extensions) error {
	// Validate the dump (and capture its TOC) BEFORE dropping anything. A
	// missing or corrupt dump fails here, leaving the local db untouched.
	toc, err := p.listDump(dumpPath)
	if err != nil {
		return err
	}
	if err := p.dropAndCreateLocal(target); err != nil {
		return err
	}
	if err := p.requireExtensions(target, exts.Require); err != nil {
		return err
	}
	useList := ""
	if len(exts.Strip) > 0 {
		filtered, changed := commentStripped(string(toc), exts.Strip)
		if changed {
			if err := os.WriteFile(listPath, []byte(filtered), 0o644); err != nil {
				return fmt.Errorf("writing restore list: %w", err)
			}
			useList = listPath
		}
	}
	return p.restoreLocal(target, dumpPath, useList)
}

// listDump runs `pg_restore -l` over a dump. This both validates the archive
// (a corrupt or incomplete dump fails here) and returns its table-of-contents
// for extension stripping.
func (p *Puller) listDump(dumpPath string) ([]byte, error) {
	return p.outputStage("validate", "pg_restore", "-l", dumpPath)
}

func (p *Puller) dropAndCreateLocal(target string) error {
	if !dbIdentifierRe.MatchString(target) {
		return fmt.Errorf("invalid database identifier: %q", target)
	}
	// Terminate active connections so the drop isn't blocked by a running app.
	// SAFETY: target is validated by dbIdentifierRe above (same pattern as
	// PostgreSQL.Clone / Rename), so it cannot break out of the SQL literal.
	terminateSQL := fmt.Sprintf(
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();",
		target,
	)
	_ = p.runStage("terminate", nil, "psql", p.localArgs("-d", "postgres", "-c", terminateSQL)...)

	if err := p.runStage("drop", nil, "dropdb", p.localArgs("--if-exists", target)...); err != nil {
		return err
	}
	return p.runStage("create", nil, "createdb", p.localArgs(target)...)
}

func (p *Puller) requireExtensions(target string, exts []string) error {
	for _, ext := range exts {
		if !extNameRe.MatchString(ext) {
			return fmt.Errorf("invalid extension name: %q", ext)
		}
		sql := fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %q;", ext)
		if err := p.runStage("extension", nil, "psql",
			p.localArgs("-v", "ON_ERROR_STOP=1", "-d", target, "-c", sql)...); err != nil {
			return err
		}
	}
	return nil
}

func (p *Puller) restoreLocal(target, dumpPath, listPath string) error {
	args := []string{"--no-owner", "--no-acl"}
	if listPath != "" {
		args = append(args, "--use-list="+listPath)
	}
	args = append(args, "-d", target, dumpPath)
	return p.runStage("restore", nil, "pg_restore", p.localArgs(args...)...)
}

// --- exec plumbing ---

// localArgs returns a fresh slice of LocalConnArgs followed by args.
func (p *Puller) localArgs(args ...string) []string {
	out := make([]string, 0, len(p.LocalConnArgs)+len(args))
	out = append(out, p.LocalConnArgs...)
	return append(out, args...)
}

// runStage runs a command, streaming output while capturing stderr for error
// classification. A non-nil env is passed via the process environment (used
// only by the remote pg_dump). args must already include any LocalConnArgs.
func (p *Puller) runStage(stage string, env []string, name string, args ...string) error {
	p.debug(env, name, args)
	if env == nil && p.execRun != nil {
		if err := p.execRun(name, args...); err != nil {
			return &ExecError{Stage: stage, Err: err}
		}
		return nil
	}
	if env != nil && p.execRunEnv != nil {
		if err := p.execRunEnv(env, name, args...); err != nil {
			return &ExecError{Stage: stage, Err: err}
		}
		return nil
	}
	cmd := exec.Command(name, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		return &ExecError{Stage: stage, Output: strings.TrimSpace(stderr.String()), Err: err}
	}
	return nil
}

func (p *Puller) outputStage(stage, name string, args ...string) ([]byte, error) {
	p.debug(nil, name, args)
	if p.execOutput != nil {
		out, err := p.execOutput(name, args...)
		if err != nil {
			return nil, &ExecError{Stage: stage, Output: strings.TrimSpace(string(out)), Err: err}
		}
		return out, nil
	}
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return nil, &ExecError{Stage: stage, Output: strings.TrimSpace(string(out)), Err: err}
	}
	return out, nil
}

func (p *Puller) debug(env []string, name string, args []string) {
	if p.Logf == nil {
		return
	}
	prefix := ""
	if len(env) > 0 {
		redacted := make([]string, len(env))
		for i, e := range env {
			if strings.HasPrefix(e, "PGPASSWORD=") {
				redacted[i] = "PGPASSWORD=***"
			} else {
				redacted[i] = e
			}
		}
		prefix = strings.Join(redacted, " ") + " "
	}
	p.Logf("exec: %s%s %s", prefix, name, strings.Join(args, " "))
}

func remoteEnv(r *RemoteConn) []string {
	ssl := r.SSLMode
	if ssl == "" {
		ssl = "require"
	}
	return []string{"PGPASSWORD=" + r.Password, "PGSSLMODE=" + ssl}
}

// commentStripped comments out (with a leading "; ") every non-comment
// EXTENSION TOC line whose extension name is in strip. It returns the rewritten
// list and whether any line changed.
func commentStripped(toc string, strip []string) (string, bool) {
	stripSet := make(map[string]bool, len(strip))
	for _, s := range strip {
		stripSet[s] = true
	}
	lines := strings.Split(toc, "\n")
	changed := false
	for i, line := range lines {
		if isStripExtensionLine(line, stripSet) {
			lines[i] = "; " + line
			changed = true
		}
	}
	return strings.Join(lines, "\n"), changed
}

func isStripExtensionLine(line string, stripSet map[string]bool) bool {
	if strings.HasPrefix(strings.TrimSpace(line), ";") {
		return false // header or already-commented entry
	}
	fields := strings.Fields(line)
	hasExtension := false
	matchesStrip := false
	for _, f := range fields {
		switch {
		case f == "EXTENSION":
			hasExtension = true
		case stripSet[f]:
			matchesStrip = true
		}
	}
	return hasExtension && matchesStrip
}
