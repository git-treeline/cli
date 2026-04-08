package database

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("creating target directory: %w", err)
	}

	src, err := os.Open(template)
	if err != nil {
		return fmt.Errorf("opening template database %s: %w", template, err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("creating target database %s: %w", target, err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copying database %s -> %s: %w", template, target, err)
	}

	return dst.Close()
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
