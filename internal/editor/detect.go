package editor

import (
	"os"
	"os/exec"
	"runtime"
)

// EditorInfo describes a detected or configured editor.
type EditorInfo struct {
	Name    string // canonical key: "cursor", "vscode", "zed", "goland", etc.
	Display string // human label: "Cursor", "VS Code", "Zed", "GoLand"
	CLI     string // command to open a path: "cursor", "code", "zed", "goland"
}

var knownEditors = map[string]EditorInfo{
	"cursor":   {Name: "cursor", Display: "Cursor", CLI: "cursor"},
	"vscode":   {Name: "vscode", Display: "VS Code", CLI: "code"},
	"zed":      {Name: "zed", Display: "Zed", CLI: "zed"},
	"goland":   {Name: "goland", Display: "GoLand", CLI: "goland"},
	"webstorm": {Name: "webstorm", Display: "WebStorm", CLI: "webstorm"},
	"rubymine": {Name: "rubymine", Display: "RubyMine", CLI: "rubymine"},
	"idea":     {Name: "idea", Display: "IntelliJ IDEA", CLI: "idea"},
	"pycharm":  {Name: "pycharm", Display: "PyCharm", CLI: "pycharm"},
	"phpstorm": {Name: "phpstorm", Display: "PhpStorm", CLI: "phpstorm"},
	"rider":    {Name: "rider", Display: "Rider", CLI: "rider"},
	"fleet":    {Name: "fleet", Display: "Fleet", CLI: "fleet"},
	"sublime":  {Name: "sublime", Display: "Sublime Text", CLI: "subl"},
	"neovim":   {Name: "neovim", Display: "Neovim", CLI: "nvim"},
	"vim":      {Name: "vim", Display: "Vim", CLI: "vim"},
	"emacs":    {Name: "emacs", Display: "Emacs", CLI: "emacs"},
	"windsurf": {Name: "windsurf", Display: "Windsurf", CLI: "windsurf"},
}

// LookupEditor returns info for a known editor name, or nil if unknown.
func LookupEditor(name string) *EditorInfo {
	if info, ok := knownEditors[name]; ok {
		return &info
	}
	return nil
}

// DetectEditor tries to identify which editor is running this terminal.
// Returns the canonical name or empty string if detection fails.
func DetectEditor() string {
	if name := detectFromEnv(); name != "" {
		return name
	}
	return detectFromPath()
}

func detectFromEnv() string {
	// Cursor sets these; VS Code does not
	if os.Getenv("CURSOR_TRACE_DIR") != "" || os.Getenv("CURSOR_CHANNEL") != "" {
		return "cursor"
	}

	// VS Code terminal (without Cursor markers)
	if os.Getenv("TERM_PROGRAM") == "vscode" {
		return "vscode"
	}

	// Zed terminal
	if os.Getenv("ZED_TERM") != "" || os.Getenv("TERM_PROGRAM") == "zed" {
		return "zed"
	}

	// JetBrains terminals set TERMINAL_EMULATOR=JetBrains-*
	if te := os.Getenv("TERMINAL_EMULATOR"); len(te) > 10 && te[:10] == "JetBrains-" {
		product := te[10:]
		return jetbrainsProduct(product)
	}

	// Windsurf (Codeium fork of VS Code)
	if os.Getenv("WINDSURF_TRACE_DIR") != "" {
		return "windsurf"
	}

	return ""
}

func jetbrainsProduct(product string) string {
	switch product {
	case "GoLand":
		return "goland"
	case "WebStorm":
		return "webstorm"
	case "RubyMine":
		return "rubymine"
	case "IntelliJIdea":
		return "idea"
	case "PyCharm":
		return "pycharm"
	case "PhpStorm":
		return "phpstorm"
	case "Rider":
		return "rider"
	case "Fleet":
		return "fleet"
	default:
		return "idea"
	}
}

// detectFromPath checks which editor CLIs are available on PATH.
// Ordered by likelihood for typical treeline users.
func detectFromPath() string {
	// Only probe PATH on macOS/Linux — reasonable dev environments
	if runtime.GOOS == "windows" {
		return ""
	}

	candidates := []string{"cursor", "code", "zed", "goland", "webstorm", "rubymine", "idea"}
	for _, cli := range candidates {
		if _, err := exec.LookPath(cli); err == nil {
			return cliToName(cli)
		}
	}
	return ""
}

func cliToName(cli string) string {
	switch cli {
	case "code":
		return "vscode"
	case "subl":
		return "sublime"
	case "nvim":
		return "neovim"
	default:
		// Most editors use their name as the CLI command
		if _, ok := knownEditors[cli]; ok {
			return cli
		}
		return ""
	}
}
