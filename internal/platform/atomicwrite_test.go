package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// AtomicWriteFile must land the full contents at the target path with the
// requested permissions, and never leave a temp file behind.
func TestAtomicWriteFile_WritesAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	if err := AtomicWriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading result: %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("content = %q, want %q", data, "hello\n")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the target file in %s, got %d entries", dir, len(entries))
	}
}

// Overwriting must replace the previous contents wholesale — readers see the
// old file or the new one, never a mix.
func TestAtomicWriteFile_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	if err := os.WriteFile(path, []byte("old contents that are longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Errorf("content = %q, want %q", data, "new")
	}
}

// A missing parent directory is the caller's responsibility; the error must
// surface rather than being swallowed.
func TestAtomicWriteFile_MissingDirErrors(t *testing.T) {
	err := AtomicWriteFile(filepath.Join(t.TempDir(), "nope", "out.json"), []byte("x"), 0o644)
	if err == nil {
		t.Fatal("expected error for missing parent dir")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected ErrNotExist, got %v", err)
	}
}
