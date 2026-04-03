package database

import (
	"os"
	"path/filepath"
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
