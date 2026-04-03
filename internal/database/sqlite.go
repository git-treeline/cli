package database

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type SQLite struct{}

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
	defer src.Close()

	dst, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("creating target database %s: %w", target, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copying database %s -> %s: %w", template, target, err)
	}

	return nil
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

func removeIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
