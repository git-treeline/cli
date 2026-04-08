package database

import (
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

	_ = os.WriteFile(template, []byte("SQLite format 3\x00fake-db-content"), 0o644)

	s := &SQLite{}
	if err := s.Clone(template, target); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal("cloned file should exist")
	}
	if string(data) != "SQLite format 3\x00fake-db-content" {
		t.Error("cloned file content doesn't match template")
	}
}

func TestSQLite_Clone_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	template := filepath.Join(dir, "template.db")
	target := filepath.Join(dir, "deep", "nested", "dir", "clone.db")

	_ = os.WriteFile(template, []byte("data"), 0o644)

	s := &SQLite{}
	if err := s.Clone(template, target); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(target); err != nil {
		t.Fatal("expected target file to exist in nested directory")
	}
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
