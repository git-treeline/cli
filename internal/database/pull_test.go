package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type pullCall struct {
	env  []string
	name string
	args []string
}

// testPuller returns a Puller whose exec seams record calls. The env-aware
// seam writes a dummy file at any "-f <path>" target so Dump's non-empty guard
// passes. failStage (matched against the recorded ExecError stage via command
// name) is not used here; per-call failure is handled by failCmd.
func testPuller(t *testing.T, connArgs []string, listOutput, failCmd string) (*Puller, *[]pullCall) {
	t.Helper()
	var calls []pullCall
	record := func(env []string, name string, args ...string) error {
		calls = append(calls, pullCall{env: env, name: name, args: args})
		// emulate pg_dump writing its output file
		if name == "pg_dump" {
			for i := 0; i < len(args)-1; i++ {
				if args[i] == "-f" {
					_ = os.WriteFile(args[i+1], []byte("PGDMP\x00data"), 0o644)
				}
			}
		}
		if name == failCmd {
			return fmt.Errorf("mock: %s failed", name)
		}
		return nil
	}
	p := &Puller{
		LocalConnArgs: connArgs,
		execRun: func(name string, args ...string) error {
			return record(nil, name, args...)
		},
		execRunEnv: func(env []string, name string, args ...string) error {
			return record(env, name, args...)
		},
		execOutput: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, pullCall{name: name, args: args})
			if name == failCmd {
				return nil, fmt.Errorf("mock: %s failed", name)
			}
			return []byte(listOutput), nil
		},
	}
	return p, &calls
}

func findCall(calls []pullCall, name string) *pullCall {
	for i := range calls {
		if calls[i].name == name {
			return &calls[i]
		}
	}
	return nil
}

func TestPuller_Dump_PasswordInEnvNotArgs(t *testing.T) {
	dir := t.TempDir()
	dumpPath := filepath.Join(dir, "production.dump")
	p, calls := testPuller(t, nil, "", "")

	remote := &RemoteConn{Host: "h", Port: "6432", User: "u", Password: "s3cret", DBName: "club", SSLMode: "require"}
	if err := p.Dump(remote, dumpPath); err != nil {
		t.Fatal(err)
	}

	c := findCall(*calls, "pg_dump")
	if c == nil {
		t.Fatal("no pg_dump call recorded")
	}
	// pg_dump writes to a temp path; the verified dump is renamed into place.
	wantArgs := []string{"-Fc", "-h", "h", "-p", "6432", "-U", "u", "-d", "club", "-f", dumpPath + ".partial"}
	if strings.Join(c.args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("pg_dump args = %v, want %v", c.args, wantArgs)
	}
	// the load-bearing assertion: password is in env, never in argv
	for _, a := range c.args {
		if strings.Contains(a, "s3cret") {
			t.Errorf("password leaked into argv: %v", c.args)
		}
	}
	if !containsEnv(c.env, "PGPASSWORD=s3cret") {
		t.Errorf("PGPASSWORD not in env: %v", c.env)
	}
	if !containsEnv(c.env, "PGSSLMODE=require") {
		t.Errorf("PGSSLMODE not in env: %v", c.env)
	}
	// the final, verified dump is in place and the partial is gone
	if _, err := os.Stat(dumpPath); err != nil {
		t.Errorf("final dump not renamed into place: %v", err)
	}
	if _, err := os.Stat(dumpPath + ".partial"); err == nil {
		t.Error("partial dump should be removed after rename")
	}
}

func TestPuller_Dump_CorruptArchiveNotRetained(t *testing.T) {
	dir := t.TempDir()
	dumpPath := filepath.Join(dir, "production.dump")
	// pg_dump "succeeds" and writes a file, but pg_restore -l (validate) fails.
	p, _ := testPuller(t, nil, "", "pg_restore")
	err := p.Dump(&RemoteConn{Host: "h", Port: "5432", User: "u", DBName: "club"}, dumpPath)
	if err == nil {
		t.Fatal("expected validation failure")
	}
	var ee *ExecError
	if !errors.As(err, &ee) || ee.Stage != "validate" {
		t.Fatalf("want validate-stage ExecError, got %v", err)
	}
	if _, err := os.Stat(dumpPath); err == nil {
		t.Error("corrupt dump must not be retained as a sample")
	}
	if _, err := os.Stat(dumpPath + ".partial"); err == nil {
		t.Error("partial dump must be cleaned up on validation failure")
	}
}

func TestPuller_Dump_EmptyFileFails(t *testing.T) {
	dir := t.TempDir()
	dumpPath := filepath.Join(dir, "x.dump")
	// execRunEnv that does NOT write a file
	p := &Puller{
		execRunEnv: func(env []string, name string, args ...string) error { return nil },
	}
	if err := p.Dump(&RemoteConn{Host: "h", Port: "5432", User: "u", DBName: "db"}, dumpPath); err == nil {
		t.Fatal("expected error when dump file is missing/empty")
	}
}

func TestPuller_DropAndCreate_Ordering(t *testing.T) {
	p, calls := testPuller(t, []string{"-h", "localhost"}, "", "")
	if err := p.dropAndCreateLocal("club_feat"); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for _, c := range *calls {
		order = append(order, c.name)
	}
	want := []string{"psql", "dropdb", "createdb"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
	// LocalConnArgs prepended on every call
	for _, c := range *calls {
		if len(c.args) < 2 || c.args[0] != "-h" || c.args[1] != "localhost" {
			t.Errorf("%s missing conn args: %v", c.name, c.args)
		}
	}
	drop := findCall(*calls, "dropdb")
	if strings.Join(drop.args, " ") != "-h localhost --if-exists club_feat" {
		t.Errorf("dropdb args = %v", drop.args)
	}
}

func TestPuller_RequireExtensions(t *testing.T) {
	p, calls := testPuller(t, nil, "", "")
	if err := p.requireExtensions("club_feat", []string{"pg_trgm", "citext"}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("want 2 psql calls, got %d", len(*calls))
	}
	first := (*calls)[0]
	joined := strings.Join(first.args, " ")
	if !strings.Contains(joined, "ON_ERROR_STOP=1") || !strings.Contains(joined, "-d club_feat") ||
		!strings.Contains(joined, `CREATE EXTENSION IF NOT EXISTS "pg_trgm"`) {
		t.Errorf("unexpected extension args: %v", first.args)
	}
}

func TestPuller_RequireExtensions_RejectsBadName(t *testing.T) {
	p, calls := testPuller(t, nil, "", "")
	if err := p.requireExtensions("club_feat", []string{"bad;name"}); err == nil {
		t.Fatal("expected error for invalid extension name")
	}
	if len(*calls) != 0 {
		t.Errorf("no command should run for invalid name, got %v", *calls)
	}
}

func TestPuller_Restore_Args(t *testing.T) {
	p, calls := testPuller(t, nil, "", "")
	if err := p.restoreLocal("club_feat", "/tmp/x.dump", "/tmp/x.toc"); err != nil {
		t.Fatal(err)
	}
	c := findCall(*calls, "pg_restore")
	want := "--no-owner --no-acl --use-list=/tmp/x.toc -d club_feat /tmp/x.dump"
	if strings.Join(c.args, " ") != want {
		t.Errorf("pg_restore args = %v, want %q", c.args, want)
	}
}

func TestPuller_Restore_NoListWhenEmpty(t *testing.T) {
	p, calls := testPuller(t, nil, "", "")
	if err := p.restoreLocal("club_feat", "/tmp/x.dump", ""); err != nil {
		t.Fatal(err)
	}
	c := findCall(*calls, "pg_restore")
	if strings.Contains(strings.Join(c.args, " "), "--use-list") {
		t.Errorf("should omit --use-list when no list path: %v", c.args)
	}
}

const sampleTOC = `;
; Archive created at 2026-06-11
;     dbname: club
;
; Selected TOC Entries:
;
215; 3079 16420 EXTENSION - pg_trgm
216; 3079 16500 EXTENSION - pgaudit
220; 1259 16600 TABLE public users app
230; 1259 16700 INDEX public idx_users app
`

func TestCommentStripped(t *testing.T) {
	out, changed := commentStripped(sampleTOC, []string{"pgaudit"})
	if !changed {
		t.Fatal("expected changed=true")
	}
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if strings.Contains(l, "pgaudit") && !strings.HasPrefix(l, "; ") {
			t.Errorf("pgaudit line not commented: %q", l)
		}
		if strings.Contains(l, "pg_trgm") && strings.HasPrefix(strings.TrimSpace(l), ";") {
			t.Errorf("pg_trgm should be left intact: %q", l)
		}
		if strings.Contains(l, "TABLE public users") && strings.HasPrefix(l, "; ") {
			t.Errorf("non-extension line should not be touched: %q", l)
		}
	}
}

func TestCommentStripped_NoMatch(t *testing.T) {
	out, changed := commentStripped(sampleTOC, []string{"nonexistent"})
	if changed {
		t.Error("expected changed=false when nothing matches")
	}
	if out != sampleTOC {
		t.Error("output should be byte-identical when nothing stripped")
	}
}

func TestPuller_Refresh_Ordering(t *testing.T) {
	p, calls := testPuller(t, nil, sampleTOC, "")
	err := p.Refresh("club_feat", "/tmp/x.dump", "/tmp/x.toc", Extensions{
		Require: []string{"pg_trgm"},
		Strip:   nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for _, c := range *calls {
		order = append(order, c.name)
	}
	// validate(pg_restore -l) BEFORE any drop, then terminate(psql), dropdb,
	// createdb, extension(psql), restore(pg_restore)
	want := []string{"pg_restore", "psql", "dropdb", "createdb", "psql", "pg_restore"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestPuller_Refresh_ValidatesBeforeDrop(t *testing.T) {
	// If the dump is unreadable, no drop/create must happen.
	p, calls := testPuller(t, nil, "", "pg_restore")
	err := p.Refresh("club_feat", "/tmp/missing.dump", "/tmp/x.toc", Extensions{})
	var ee *ExecError
	if !errors.As(err, &ee) || ee.Stage != "validate" {
		t.Fatalf("want validate-stage ExecError, got %v", err)
	}
	for _, c := range *calls {
		if c.name == "dropdb" || c.name == "createdb" {
			t.Errorf("must not %s when validation fails: %v", c.name, *calls)
		}
	}
}

func TestPuller_Refresh_PropagatesExecError(t *testing.T) {
	p, _ := testPuller(t, nil, "", "createdb")
	err := p.Refresh("club_feat", "/tmp/x.dump", "/tmp/x.toc", Extensions{})
	var ee *ExecError
	if !errors.As(err, &ee) {
		t.Fatalf("want *ExecError, got %T: %v", err, err)
	}
	if ee.Stage != "create" {
		t.Errorf("stage = %q, want create", ee.Stage)
	}
}

func containsEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

