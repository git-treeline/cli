package database

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLite_Clone(t *testing.T) {
	dir := t.TempDir()
	template := filepath.Join(dir, "template.db")
	target := filepath.Join(dir, "sub", "cloned.db")

	createSQLiteTable(t, template, "widgets", 42)

	s := &SQLite{}
	if err := s.Clone(template, target); err != nil {
		t.Fatal(err)
	}
	if got := sqliteValue(t, target, "select value from widgets"); got != "42" {
		t.Errorf("cloned row = %q, want 42", got)
	}
}

func TestSQLite_Clone_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	template := filepath.Join(dir, "template.db")
	target := filepath.Join(dir, "deep", "nested", "dir", "clone.db")

	createSQLiteTable(t, template, "widgets", 42)

	s := &SQLite{}
	if err := s.Clone(template, target); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(target); err != nil {
		t.Fatal("expected target file to exist in nested directory")
	}
}

func TestSQLite_Clone_IncludesCommittedWALData(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is required for SQLite cloning")
	}
	dir := t.TempDir()
	template := filepath.Join(dir, "template.db")
	target := filepath.Join(dir, "clone.db")

	cmd := exec.Command("sqlite3", template)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	if _, err := fmt.Fprintln(stdin, "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE widgets (value INTEGER); INSERT INTO widgets VALUES (42); SELECT 'ready';"); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	ready := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "ready" {
			ready = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("sqlite did not commit WAL data")
	}
	if _, err := os.Stat(template + "-wal"); err != nil {
		t.Fatalf("expected live WAL file: %v", err)
	}

	if err := (&SQLite{}).Clone(template, target); err != nil {
		t.Fatal(err)
	}
	if got := sqliteValue(t, target, "select value from widgets"); got != "42" {
		t.Errorf("WAL row = %q, want 42", got)
	}
}

func TestSQLite_Clone_HandlesQuotedAndBackslashPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), `folder "quoted" \ slash`)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(dir, `template "quoted" \ slash.db`)
	target := filepath.Join(dir, `target "quoted" \ slash.db`)
	createSQLiteTable(t, template, "widgets", 42)

	if err := (&SQLite{}).Clone(template, target); err != nil {
		t.Fatal(err)
	}
	if got := sqliteValue(t, target, "select value from widgets"); got != "42" {
		t.Errorf("cloned row = %q, want 42", got)
	}
}

func TestSQLite_Clone_RejectsTargetWithWALSidecar(t *testing.T) {
	dir := t.TempDir()
	template := filepath.Join(dir, "template.db")
	target := filepath.Join(dir, "target.db")
	createSQLiteTable(t, template, "widgets", 42)
	createSQLiteTable(t, target, "target_rows", 7)
	if err := os.WriteFile(target+"-wal", []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := (&SQLite{}).Clone(template, target)
	if err == nil || !strings.Contains(err.Error(), "active SQLite sidecar") {
		t.Fatalf("expected active sidecar error, got %v", err)
	}
	if got := sqliteValue(t, target, "select value from target_rows"); got != "7" {
		t.Errorf("existing target changed after rejected clone: %q", got)
	}
}

func TestSQLite_Clone_FailurePreservesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	template := filepath.Join(dir, "template.db")
	target := filepath.Join(dir, "target.db")
	createSQLiteTable(t, template, "source_rows", 42)
	createSQLiteTable(t, target, "target_rows", 7)

	s := &SQLite{newCommand: func(string, ...string) *exec.Cmd { return exec.Command("false") }}
	if err := s.Clone(template, target); err == nil {
		t.Fatal("expected clone failure")
	}
	if got := sqliteValue(t, target, "select value from target_rows"); got != "7" {
		t.Errorf("existing target changed after failed clone: %q", got)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".treeline-clone-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("failed clone left staging files: %v", leftovers)
	}
}

func TestSQLite_Clone_RejectsSameSourceAndTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.db")
	createSQLiteTable(t, path, "widgets", 42)
	if err := (&SQLite{}).Clone(path, path); err == nil {
		t.Fatal("expected same source and target error")
	}
	if got := sqliteValue(t, path, "select value from widgets"); got != "42" {
		t.Errorf("source changed after rejected clone: %q", got)
	}
}

func createSQLiteTable(t *testing.T, path, table string, value int) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is required for SQLite cloning")
	}
	cmd := exec.Command("sqlite3", path, fmt.Sprintf("CREATE TABLE %s (value INTEGER); INSERT INTO %s VALUES (%d);", table, table, value))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating SQLite fixture: %v: %s", err, out)
	}
}

func sqliteValue(t *testing.T, path, query string) string {
	t.Helper()
	out, err := exec.Command("sqlite3", path, query).CombinedOutput()
	if err != nil {
		t.Fatalf("querying SQLite fixture: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSQLite_Clone_MissingTemplate(t *testing.T) {
	dir := t.TempDir()
	s := &SQLite{}
	err := s.Clone(filepath.Join(dir, "nonexistent.db"), filepath.Join(dir, "target.db"))
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}

func TestSQLite_Exists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s := &SQLite{}

	exists, err := s.Exists(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected Exists=false for missing file")
	}

	_ = os.WriteFile(dbPath, []byte("data"), 0o644)

	exists, err = s.Exists(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected Exists=true for existing file")
	}
}

func TestSQLite_Drop(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"

	_ = os.WriteFile(dbPath, []byte("data"), 0o644)
	_ = os.WriteFile(walPath, []byte("wal"), 0o644)
	_ = os.WriteFile(shmPath, []byte("shm"), 0o644)

	s := &SQLite{}
	if err := s.Drop(dbPath); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{dbPath, walPath, shmPath} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("expected %s to be removed", path)
		}
	}
}

func TestSQLite_Drop_Nonexistent(t *testing.T) {
	dir := t.TempDir()
	s := &SQLite{}
	if err := s.Drop(filepath.Join(dir, "nonexistent.db")); err != nil {
		t.Errorf("dropping nonexistent file should not error: %v", err)
	}
}

// --- SQLite.Restore tests ---

func TestSQLite_Restore_Success(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "restored.db")
	dumpFile := filepath.Join(dir, "dump.sql")
	dumpContent := "CREATE TABLE foo (id INTEGER);"
	_ = os.WriteFile(dumpFile, []byte(dumpContent), 0o644)

	// Use "cat" as a fake sqlite3 — it reads stdin and writes to stdout.
	// We redirect stdout to a capture file to verify stdin was piped.
	stdinCapture := filepath.Join(dir, "stdin_capture")
	var calledName string
	var calledArgs []string
	s := &SQLite{
		newCommand: func(name string, args ...string) *exec.Cmd {
			calledName = name
			calledArgs = args
			return exec.Command("sh", "-c", fmt.Sprintf("cat > %s", stdinCapture))
		},
	}

	err := s.Restore(target, dumpFile)
	if err != nil {
		t.Fatal(err)
	}

	if calledName != "sqlite3" {
		t.Errorf("expected sqlite3 command, got %q", calledName)
	}
	if len(calledArgs) != 1 || calledArgs[0] != target {
		t.Errorf("expected args [%s], got %v", target, calledArgs)
	}

	// Verify dump file contents were actually piped to the command's stdin
	captured, err := os.ReadFile(stdinCapture)
	if err != nil {
		t.Fatalf("stdin capture file not written: %v", err)
	}
	if string(captured) != dumpContent {
		t.Errorf("stdin received %q, want %q", string(captured), dumpContent)
	}
}

func TestSQLite_Restore_DropsExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.db")
	walPath := target + "-wal"
	dumpFile := filepath.Join(dir, "dump.sql")

	_ = os.WriteFile(target, []byte("old data"), 0o644)
	_ = os.WriteFile(walPath, []byte("wal"), 0o644)
	_ = os.WriteFile(dumpFile, []byte("CREATE TABLE foo;"), 0o644)

	s := &SQLite{
		newCommand: func(name string, args ...string) *exec.Cmd {
			return exec.Command("true")
		},
	}

	err := s.Restore(target, dumpFile)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(target); err == nil {
		t.Error("expected old database file to be dropped before restore")
	}
	if _, err := os.Stat(walPath); err == nil {
		t.Error("expected WAL file to be dropped before restore")
	}
}

func TestSQLite_Restore_MissingDumpFile(t *testing.T) {
	dir := t.TempDir()
	s := &SQLite{
		newCommand: func(name string, args ...string) *exec.Cmd {
			return exec.Command("true")
		},
	}

	err := s.Restore(filepath.Join(dir, "target.db"), filepath.Join(dir, "nonexistent.sql"))
	if err == nil {
		t.Fatal("expected error for missing dump file")
	}
	if !strings.Contains(err.Error(), "opening dump file") {
		t.Errorf("expected 'opening dump file' in error, got: %v", err)
	}
}

func TestSQLite_Restore_CommandFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	dumpFile := filepath.Join(dir, "dump.sql")
	_ = os.WriteFile(dumpFile, []byte("CREATE TABLE foo;"), 0o644)

	s := &SQLite{
		newCommand: func(name string, args ...string) *exec.Cmd {
			return exec.Command("false")
		},
	}

	err := s.Restore(target, dumpFile)
	if err == nil {
		t.Fatal("expected error when sqlite3 fails")
	}
	if !strings.Contains(err.Error(), "restoring") {
		t.Errorf("expected 'restoring' in error, got: %v", err)
	}
}

func TestSQLite_Rename_MovesFile(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.db")
	newPath := filepath.Join(dir, "new.db")
	walPath := oldPath + "-wal"
	shmPath := oldPath + "-shm"

	_ = os.WriteFile(oldPath, []byte("data"), 0o644)
	_ = os.WriteFile(walPath, []byte("wal"), 0o644)
	_ = os.WriteFile(shmPath, []byte("shm"), 0o644)

	s := &SQLite{}
	if err := s.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("expected new path to exist: %v", err)
	}
	if _, err := os.Stat(newPath + "-wal"); err != nil {
		t.Errorf("expected new wal path to exist: %v", err)
	}
	if _, err := os.Stat(oldPath); err == nil {
		t.Error("expected old path to be gone")
	}
}

func TestSQLite_Rename_TargetExists_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.db")
	newPath := filepath.Join(dir, "new.db")

	_ = os.WriteFile(oldPath, []byte("old data"), 0o644)
	_ = os.WriteFile(newPath, []byte("existing data"), 0o644)

	s := &SQLite{}
	err := s.Rename(oldPath, newPath)
	if err == nil {
		t.Fatal("expected error when target exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error, got: %v", err)
	}

	// Original file must be untouched.
	data, _ := os.ReadFile(newPath)
	if string(data) != "existing data" {
		t.Error("existing target was clobbered")
	}
}

func TestSQLite_Rename_MissingSource_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "missing.db")
	newPath := filepath.Join(dir, "new.db")

	s := &SQLite{}
	err := s.Rename(oldPath, newPath)
	if err == nil {
		t.Fatal("expected error when source is missing")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected not-exist error, got: %v", err)
	}
	if _, statErr := os.Stat(newPath); !os.IsNotExist(statErr) {
		t.Errorf("target should not be created, stat err=%v", statErr)
	}
}
