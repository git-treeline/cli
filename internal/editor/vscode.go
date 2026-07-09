package editor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VSCodeSettings holds the settings to write for a VS Code/Cursor workspace.
type VSCodeSettings struct {
	Title string // window.title value
	Color string // hex color for title/activity/status bars (empty = no color)
	Theme string // workbench.colorTheme override (empty = no override)
}

// WriteVSCode writes editor settings to the appropriate location.
// If a .code-workspace file references the worktree, settings go there.
// Otherwise, settings go to .vscode/settings.json in the worktree.
func WriteVSCode(worktreePath string, s VSCodeSettings) (string, error) {
	if wsPath := findWorkspaceFile(worktreePath); wsPath != "" {
		return wsPath, writeToWorkspaceFile(wsPath, s)
	}
	return writeToVSCodeDir(worktreePath, s)
}

func buildSettings(s VSCodeSettings) map[string]any {
	settings := make(map[string]any)

	if s.Title != "" {
		settings["window.title"] = s.Title
	}

	if s.Theme != "" {
		settings["workbench.colorTheme"] = s.Theme
	}

	if s.Color != "" {
		fg := ForegroundForHex(s.Color)
		inactiveBg := DarkenHex(s.Color)
		inactiveFg := "#cccccc"
		if fg != "#ffffff" {
			inactiveFg = "#555555"
		}

		settings["workbench.colorCustomizations"] = map[string]string{
			"titleBar.activeBackground":   s.Color,
			"titleBar.activeForeground":   fg,
			"titleBar.inactiveBackground": inactiveBg,
			"titleBar.inactiveForeground": inactiveFg,
			"activityBar.background":      s.Color,
			"activityBar.foreground":      fg,
			"statusBar.background":        s.Color,
			"statusBar.foreground":        fg,
		}
	}

	return settings
}

func writeToVSCodeDir(worktreePath string, s VSCodeSettings) (string, error) {
	dir := filepath.Join(worktreePath, ".vscode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating .vscode directory: %w", err)
	}
	path := filepath.Join(dir, "settings.json")

	existing, err := loadJSON(path)
	if err != nil {
		return "", fmt.Errorf("refusing to update %s: not valid JSON (VS Code JSONC with comments is not supported): %w — add the settings manually or remove the comments", path, err)
	}
	merged := mergeSettings(existing, buildSettings(s))

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(data, '\n'), 0o644)
}

func writeToWorkspaceFile(wsPath string, s VSCodeSettings) error {
	raw, err := os.ReadFile(wsPath)
	if err != nil {
		return err
	}

	var ws map[string]any
	if err := json.Unmarshal(raw, &ws); err != nil {
		return err
	}

	settings, _ := ws["settings"].(map[string]any)
	if settings == nil {
		settings = make(map[string]any)
	}

	merged := mergeSettings(settings, buildSettings(s))
	ws["settings"] = merged

	data, err := json.MarshalIndent(ws, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(wsPath, append(data, '\n'), 0o644)
}

// findWorkspaceFile walks up from worktreePath looking for a .code-workspace
// file that references this folder.
func findWorkspaceFile(worktreePath string) string {
	absPath, err := filepath.Abs(worktreePath)
	if err != nil {
		return ""
	}

	dir := filepath.Dir(absPath)

	for i := 0; i < 5; i++ {
		entries, err := os.ReadDir(dir)
		if err != nil {
			break
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".code-workspace") {
				continue
			}
			wsPath := filepath.Join(dir, e.Name())
			if workspaceContainsFolder(wsPath, absPath) {
				return wsPath
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func workspaceContainsFolder(wsPath, absPath string) bool {
	raw, err := os.ReadFile(wsPath)
	if err != nil {
		return false
	}
	var ws struct {
		Folders []struct {
			Path string `json:"path"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(raw, &ws); err != nil {
		return false
	}
	wsDir := filepath.Dir(wsPath)
	for _, f := range ws.Folders {
		folderAbs := f.Path
		if !filepath.IsAbs(folderAbs) {
			folderAbs = filepath.Join(wsDir, f.Path)
		}
		resolved, err := filepath.Abs(folderAbs)
		if err != nil {
			continue
		}
		if resolved == absPath {
			return true
		}
	}
	return false
}

// loadJSON reads and parses a JSON file. A missing file is not an error (it
// simply hasn't been created yet), but a file that exists and fails to parse
// returns an error rather than silently discarding its contents — VS Code
// settings.json is JSONC (comments, trailing commas) which encoding/json
// cannot parse, and treating that as "empty" would cause writeToVSCodeDir to
// overwrite the user's real settings.
func loadJSON(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func mergeSettings(base, overlay map[string]any) map[string]any {
	for k, v := range overlay {
		base[k] = v
	}
	return base
}
