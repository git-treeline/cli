package database

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type SQLite struct {
	newCommand func(name string, args ...string) *exec.Cmd
}

func (s *SQLite) command(name string, args ...string) *exec.Cmd {
	if s.newCommand != nil {
		return s.newCommand(name, args...)
	}
	return exec.Command(name, args...)
}

func (s *SQLite) Exists(name string) (bool, error) {
	_, err := os.Stat(name)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *SQLite) Clone(template, target string) error {
	templatePath, err := filepath.Abs(template)
	if err != nil {
		return fmt.Errorf("resolving template database %s: %w", template, err)
	}
	targetPath, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolving target database %s: %w", target, err)
	}
	if templatePath == targetPath {
		return fmt.Errorf("template and target database are the same: %s", template)
	}
	if _, err := os.Stat(templatePath); err != nil {
		return fmt.Errorf("opening template database %s: %w", template, err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("creating target directory: %w", err)
	}
	for _, sidecar := range []string{targetPath + "-wal", targetPath + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			return fmt.Errorf("target database has an active SQLite sidecar: %s", sidecar)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking target database sidecar %s: %w", sidecar, err)
		}
	}

	backupPath, err := os.CreateTemp(filepath.Dir(targetPath), ".treeline-clone-*")
	if err != nil {
		return fmt.Errorf("creating target database %s: %w", target, err)
	}
	backupName := backupPath.Name()
	if err := backupPath.Close(); err != nil {
		_ = os.Remove(backupName)
		return fmt.Errorf("preparing target database %s: %w", target, err)
	}
	defer func() { _ = os.Remove(backupName) }()

	quotedTarget, err := sqliteCommandArg(backupName)
	if err != nil {
		return err
	}
	cmd := s.command("sqlite3", templatePath, ".backup \""+quotedTarget+"\"")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cloning database %s -> %s: %w", template, target, err)
	}
	if err := os.Rename(backupName, targetPath); err != nil {
		return fmt.Errorf("installing cloned database %s: %w", target, err)
	}
	return nil
}

// sqliteCommandArg returns a double-quoted sqlite shell argument. The backup
// filename is interpreted by sqlite3's dot-command parser, not by a shell, so
// escape its parser's two special characters before embedding it in .backup.
func sqliteCommandArg(path string) (string, error) {
	if strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("SQLite database path contains unsupported control characters: %q", path)
	}
	path = strings.ReplaceAll(path, `\`, `\\`)
	return strings.ReplaceAll(path, `"`, `\"`), nil
}

// Create creates an empty database file — the degraded fallback when the
// configured template file is missing.
func (s *SQLite) Create(name string) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return fmt.Errorf("creating target directory: %w", err)
	}
	f, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("creating database %s: %w", name, err)
	}
	return f.Close()
}

func (s *SQLite) Drop(target string) error {
	if err := removeIfExists(target); err != nil {
		return err
	}
	// SQLite WAL mode companion files
	_ = removeIfExists(target + "-wal")
	_ = removeIfExists(target + "-shm")
	return nil
}

func (s *SQLite) Rename(oldPath, newPath string) error {
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return fmt.Errorf("creating target directory: %w", err)
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("target database already exists: %s", newPath)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	// Best-effort rename of WAL mode companion files.
	_ = os.Rename(oldPath+"-wal", newPath+"-wal")
	_ = os.Rename(oldPath+"-shm", newPath+"-shm")
	return nil
}

func (s *SQLite) Restore(target, dumpFile string) error {
	if err := s.Drop(target); err != nil {
		return err
	}
	dump, err := os.Open(dumpFile)
	if err != nil {
		return fmt.Errorf("opening dump file %s: %w", dumpFile, err)
	}
	defer func() { _ = dump.Close() }()

	cmd := s.command("sqlite3", target)
	cmd.Stdin = dump
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restoring %s into %s: %w", dumpFile, target, err)
	}
	return nil
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
