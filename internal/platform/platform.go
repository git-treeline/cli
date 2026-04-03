// Package platform provides platform-specific configuration paths.
// On macOS: ~/Library/Application Support/git-treeline/
// On Linux: $XDG_CONFIG_HOME/git-treeline/ (or ~/.config/)
// On Windows: %APPDATA%/git-treeline/
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

const appName = "git-treeline"

func ConfigDir() string {
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
