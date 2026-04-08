package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePsqlListContains(t *testing.T) {
	// Realistic psql -lqt output
	output := ` myapp_development   | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           |
 myapp_test          | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           |
 postgres            | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           |
 template0           | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           | =c/user          +
                     |      |          |                 |             |             |            |           | user=CTc/user
 template1           | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           | =c/user          +
                     |      |          |                 |             |             |            |           | user=CTc/user
`

	tests := []struct {
		name   string
		db     string
		expect bool
	}{
		{"existing db", "myapp_development", true},
		{"another existing db", "myapp_test", true},
		{"system db", "postgres", true},
		{"nonexistent", "myapp_staging", false},
		{"partial match", "myapp", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePsqlListContains(output, tt.db)
			if got != tt.expect {
				t.Errorf("parsePsqlListContains(%q) = %v, want %v", tt.db, got, tt.expect)
			}
		})
	}
}

func TestIsCustomFormat_PGDMP(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "dump.pgdmp")
	_ = os.WriteFile(f, []byte("PGDMP\x00\x00\x00more data"), 0o644)
	if !isCustomFormat(f) {
		t.Error("expected custom format for PGDMP header")
	}
}

func TestIsCustomFormat_PlainSQL(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "dump.sql")
	_ = os.WriteFile(f, []byte("-- PostgreSQL dump\nCREATE TABLE foo"), 0o644)
	if isCustomFormat(f) {
		t.Error("expected plain SQL to not be detected as custom format")
	}
}

func TestIsCustomFormat_Missing(t *testing.T) {
	if isCustomFormat("/nonexistent/dump.pgdmp") {
		t.Error("expected false for missing file")
	}
}

type cmdCall struct {
	name string
	args []string
}

func (c cmdCall) String() string {
	return c.name + " " + strings.Join(c.args, " ")
}

func testPg(t *testing.T, psqlOutput string, failCmd string) (*PostgreSQL, *[]cmdCall) {
	t.Helper()
	var calls []cmdCall
	pg := &PostgreSQL{
		execRun: func(name string, args ...string) error {
			calls = append(calls, cmdCall{name, args})
			if name == failCmd {
				return fmt.Errorf("mock: %s failed", name)
			}
			return nil
		},
		execOutput: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, cmdCall{name, args})
			if name == failCmd {
				return nil, fmt.Errorf("mock: %s failed", name)
			}
			return []byte(psqlOutput), nil
		},
	}
	return pg, &calls
}

// --- PostgreSQL.Exists tests ---

func TestPostgreSQL_Exists_Found(t *testing.T) {
	psqlOutput := " myapp_dev | user | UTF8\n postgres  | user | UTF8\n"
	pg, calls := testPg(t, psqlOutput, "")

	exists, err := pg.Exists("myapp_dev")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected Exists=true for listed database")
	}
	if len(*calls) != 1 || (*calls)[0].name != "psql" {
		t.Errorf("expected psql call, got %v", *calls)
	}
}

func TestPostgreSQL_Exists_NotFound(t *testing.T) {
	psqlOutput := " myapp_dev | user | UTF8\n"
	pg, _ := testPg(t, psqlOutput, "")

	exists, err := pg.Exists("other_db")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected Exists=false for unlisted database")
	}
}

func TestPostgreSQL_Exists_InvalidIdentifier(t *testing.T) {
	pg, calls := testPg(t, "", "")

	_, err := pg.Exists("drop table;--")
	if err == nil {
		t.Fatal("expected error for invalid identifier")
	}
	if !strings.Contains(err.Error(), "invalid database identifier") {
		t.Errorf("expected 'invalid database identifier' in error, got: %v", err)
	}
	if len(*calls) != 0 {
		t.Error("should not have called psql for invalid identifier")
	}
}

func TestPostgreSQL_Exists_PsqlFailure(t *testing.T) {
	pg, _ := testPg(t, "", "psql")

	_, err := pg.Exists("myapp")
	if err == nil {
		t.Fatal("expected error when psql fails")
	}
	if !strings.Contains(err.Error(), "failed to list databases") {
		t.Errorf("expected 'failed to list databases' in error, got: %v", err)
	}
}

// --- PostgreSQL.Clone tests ---

func TestPostgreSQL_Clone_Success(t *testing.T) {
	pg, calls := testPg(t, "", "")

	err := pg.Clone("myapp_dev", "myapp_feat")
	if err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 2 {
		t.Fatalf("expected 2 calls (terminate + createdb), got %d: %v", len(*calls), *calls)
	}
	if (*calls)[0].name != "psql" {
		t.Errorf("first call should be psql (terminate), got %s", (*calls)[0].name)
	}
	if (*calls)[1].name != "createdb" {
		t.Errorf("second call should be createdb, got %s", (*calls)[1].name)
	}
	args := (*calls)[1].args
	if len(args) != 3 || args[0] != "myapp_feat" || args[1] != "--template" || args[2] != "myapp_dev" {
		t.Errorf("createdb args = %v, want [myapp_feat --template myapp_dev]", args)
	}
}

func TestPostgreSQL_Clone_InvalidTarget(t *testing.T) {
	pg, calls := testPg(t, "", "")

	err := pg.Clone("myapp_dev", "bad;name")
	if err == nil {
		t.Fatal("expected error for invalid target")
	}
	if len(*calls) != 0 {
		t.Error("should not execute commands with invalid identifier")
	}
}

func TestPostgreSQL_Clone_InvalidTemplate(t *testing.T) {
	pg, calls := testPg(t, "", "")

	err := pg.Clone("bad;name", "myapp_feat")
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
	if len(*calls) != 0 {
		t.Error("should not execute commands with invalid identifier")
	}
}

func TestPostgreSQL_Clone_CreatedbFailure(t *testing.T) {
	pg, _ := testPg(t, "", "createdb")

	err := pg.Clone("myapp_dev", "myapp_feat")
	if err == nil {
		t.Fatal("expected error when createdb fails")
	}
	if !strings.Contains(err.Error(), "failed to clone database") {
		t.Errorf("expected 'failed to clone database' in error, got: %v", err)
	}
}

func TestPostgreSQL_Clone_TerminateFailureIgnored(t *testing.T) {
	pg, calls := testPg(t, "", "psql")

	// psql (terminate) will fail, but Clone should still proceed to createdb
	// Since both psql and createdb go through execRun and we fail "psql",
	// we need a smarter mock. Let's build one inline.
	callCount := 0
	pg.execRun = func(name string, args ...string) error {
		*calls = append(*calls, cmdCall{name, args})
		callCount++
		if name == "psql" {
			return fmt.Errorf("mock: terminate failed")
		}
		return nil
	}

	err := pg.Clone("myapp_dev", "myapp_feat")
	if err != nil {
		t.Fatalf("terminate failure should be ignored, got: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(*calls))
	}
}

// --- PostgreSQL.Drop tests ---

func TestPostgreSQL_Drop_Success(t *testing.T) {
	pg, calls := testPg(t, "", "")

	err := pg.Drop("myapp_feat")
	if err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.name != "dropdb" {
		t.Errorf("expected dropdb, got %s", c.name)
	}
	if len(c.args) != 2 || c.args[0] != "--if-exists" || c.args[1] != "myapp_feat" {
		t.Errorf("dropdb args = %v, want [--if-exists myapp_feat]", c.args)
	}
}

func TestPostgreSQL_Drop_Failure(t *testing.T) {
	pg, _ := testPg(t, "", "dropdb")

	err := pg.Drop("myapp_feat")
	if err == nil {
		t.Fatal("expected error when dropdb fails")
	}
}

func TestPostgreSQL_Drop_InvalidIdentifier(t *testing.T) {
	pg, calls := testPg(t, "", "")

	err := pg.Drop("bad;name")
	if err == nil {
		t.Fatal("expected error for invalid identifier")
	}
	if len(*calls) != 0 {
		t.Error("should not execute commands with invalid identifier")
	}
}

// --- PostgreSQL.Restore tests ---

func TestPostgreSQL_Restore_PlainSQL(t *testing.T) {
	dir := t.TempDir()
	dumpFile := filepath.Join(dir, "dump.sql")
	_ = os.WriteFile(dumpFile, []byte("CREATE TABLE foo;"), 0o644)

	pg, calls := testPg(t, "", "")

	err := pg.Restore("myapp_feat", dumpFile)
	if err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 2 {
		t.Fatalf("expected 2 calls (createdb + psql), got %d: %v", len(*calls), *calls)
	}
	if (*calls)[0].name != "createdb" {
		t.Errorf("first call should be createdb, got %s", (*calls)[0].name)
	}
	if (*calls)[1].name != "psql" {
		t.Errorf("second call should be psql for plain SQL, got %s", (*calls)[1].name)
	}
	args := (*calls)[1].args
	if len(args) != 4 || args[0] != "-d" || args[1] != "myapp_feat" || args[2] != "-f" || args[3] != dumpFile {
		t.Errorf("psql args = %v, want [-d myapp_feat -f %s]", args, dumpFile)
	}
}

func TestPostgreSQL_Restore_CustomFormat(t *testing.T) {
	dir := t.TempDir()
	dumpFile := filepath.Join(dir, "dump.pgdmp")
	_ = os.WriteFile(dumpFile, []byte("PGDMP\x00\x00\x00data"), 0o644)

	pg, calls := testPg(t, "", "")

	err := pg.Restore("myapp_feat", dumpFile)
	if err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(*calls))
	}
	if (*calls)[1].name != "pg_restore" {
		t.Errorf("second call should be pg_restore for custom format, got %s", (*calls)[1].name)
	}
	args := (*calls)[1].args
	if len(args) != 5 || args[0] != "--no-owner" || args[1] != "--no-acl" || args[2] != "-d" || args[3] != "myapp_feat" || args[4] != dumpFile {
		t.Errorf("pg_restore args = %v, want [--no-owner --no-acl -d myapp_feat %s]", args, dumpFile)
	}
}

func TestPostgreSQL_Restore_InvalidIdentifier(t *testing.T) {
	pg, calls := testPg(t, "", "")

	err := pg.Restore("bad;name", "/tmp/dump.sql")
	if err == nil {
		t.Fatal("expected error for invalid identifier")
	}
	if len(*calls) != 0 {
		t.Error("should not execute commands with invalid identifier")
	}
}

func TestPostgreSQL_Restore_CreatedbFailure(t *testing.T) {
	dir := t.TempDir()
	dumpFile := filepath.Join(dir, "dump.sql")
	_ = os.WriteFile(dumpFile, []byte("CREATE TABLE foo;"), 0o644)

	pg, calls := testPg(t, "", "createdb")

	err := pg.Restore("myapp_feat", dumpFile)
	if err == nil {
		t.Fatal("expected error when createdb fails")
	}
	if !strings.Contains(err.Error(), "creating database") {
		t.Errorf("expected 'creating database' in error, got: %v", err)
	}
	if len(*calls) != 1 {
		t.Errorf("should stop after createdb failure, got %d calls", len(*calls))
	}
}

func TestPostgreSQL_Restore_RestoreCommandFailure(t *testing.T) {
	dir := t.TempDir()
	dumpFile := filepath.Join(dir, "dump.sql")
	_ = os.WriteFile(dumpFile, []byte("CREATE TABLE foo;"), 0o644)

	pg := &PostgreSQL{
		execRun: func(name string, args ...string) error {
			if name == "psql" && len(args) > 0 && args[0] == "-d" {
				return fmt.Errorf("mock: restore failed")
			}
			return nil
		},
	}

	err := pg.Restore("myapp_feat", dumpFile)
	if err == nil {
		t.Fatal("expected error when restore command fails")
	}
	if !strings.Contains(err.Error(), "restoring") {
		t.Errorf("expected 'restoring' in error, got: %v", err)
	}
}

func TestForAdapter(t *testing.T) {
	tests := []struct {
		name      string
		wantErr   bool
		wantType  string
	}{
		{"postgresql", false, "postgresql"},
		{"sqlite", false, "sqlite"},
		{"", false, "postgresql"},
		{"mysql", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, err := ForAdapter(tt.name)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if adapter == nil {
				t.Fatal("expected non-nil adapter")
			}
		})
	}
}
