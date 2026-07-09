// Package platform provides platform-specific configuration paths.
// On macOS: ~/Library/Application Support/git-treeline/
// On Linux: $XDG_CONFIG_HOME/git-treeline/ (or ~/.config/)
// On Windows: %APPDATA%/git-treeline/
//
// Set GTL_HOME to override the config directory entirely. This is useful
// for development/testing to avoid colliding with the installed binary.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

const appName = "git-treeline"

const (
	// DirMode is the permission for the git-treeline data directory.
	// Owner-only: the directory contains credentials (redis URLs) and
	// the registry that drives proxy routing.
	DirMode os.FileMode = 0o700

	// PrivateFileMode is the permission for files that may contain
	// credentials or routing state (config.json, registry.json).
	PrivateFileMode os.FileMode = 0o600
)

// EnsureConfigDir creates the config directory with DirMode if it doesn't
// exist, and tightens permissions on an existing directory if it's too open.
func EnsureConfigDir() error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return err
	}
	return os.Chmod(dir, DirMode)
}

// IsDevMode returns true when GTL_HOME is set, indicating this instance
// should use an isolated state directory.
func IsDevMode() bool {
	return os.Getenv("GTL_HOME") != "" 
}

// DevSuffix returns ".dev" when GTL_HOME is set, empty string otherwise.
// Used by the service layer to namespace LaunchAgent labels and pf anchors.
func DevSuffix() string {
	if IsDevMode() {
		return ".dev"
	}
	return ""
}

func ConfigDir() string {
	if home := os.Getenv("GTL_HOME"); home != "" {
		return home
	}
	return filepath.Join(baseDir(), appName)
}

func ConfigFile() string {
	return filepath.Join(ConfigDir(), "config.json")
}

func RegistryFile() string {
	return filepath.Join(ConfigDir(), "registry.json")
}

func baseDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support")
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return appdata
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Roaming")
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return xdg
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config")
	}
}

// AtomicWriteFile writes data to path by creating a temp file in the same
// directory, fsyncing it, and renaming it into place. A crash or ENOSPC
// mid-write can only corrupt the discarded temp file, never the live target —
// the rename is atomic, so readers see either the old contents or the new
// ones, and the Sync guarantees the new contents are on disk before the
// rename makes them visible.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Chmod(perm)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
