// Package detect provides framework and tooling auto-detection from
// filesystem signals. It identifies frameworks (Rails, Next.js, etc.),
// package managers, database adapters, and other project characteristics
// to generate appropriate configuration templates.
package detect

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Result contains the detection findings for a project directory.
// All fields are populated by Detect() based on filesystem analysis.
type Result struct {
	Framework      string   // "nextjs", "vite", "rails", "phoenix", "node", "django", "python", "rust", "go", "unknown"
	HasPrisma      bool
	HasJSBundler   bool     // jsbundling-rails/cssbundling-rails or multi-process Procfile.dev
	HasDotenv      bool     // project has dotenv or equivalent wired up
	DBAdapter      string   // "postgresql", "sqlite", ""
	HasRedis       bool
	HasEnvFile     bool     // true if any env file exists on disk
	EnvFile        string   // best candidate: ".env.local", ".env.development", ".env", etc.
	EnvFiles       []string // all env files found, in priority order
	PackageManager string   // "npm", "yarn", "pnpm", "bundle", "mix", "cargo", "pip", ""
	IsUmbrella     bool     // Phoenix umbrella project (apps/*/mix.exs); not yet fully supported
	MergeTarget    string   // set by caller when git context is available
}

func Detect(root string) *Result {
	r := &Result{Framework: "unknown"}

	r.detectFramework(root)
	r.detectPrisma(root)
	r.detectJSBundler(root)
	r.detectDotenv(root)
	r.detectDatabase(root)
	r.detectRedis(root)
	r.detectPackageManager(root)
	r.detectEnvFile(root)

	return r
}

func (r *Result) detectFramework(root string) {
	// Most specific first
	if fileExistsAny(root, "next.config.js", "next.config.mjs", "next.config.ts") {
		r.Framework = "nextjs"
		return
	}

	if fileExists(root, "Gemfile") && fileExists(root, "config/application.rb") {
		r.Framework = "rails"
		return
	}

	if fileExists(root, "mix.exs") && r.detectPhoenix(root) {
		r.Framework = "phoenix"
		return
	}

	if fileExists(root, "manage.py") || (fileExists(root, "pyproject.toml") && dirExists(root, "templates")) {
		r.Framework = "django"
		return
	}

	if fileExists(root, "pyproject.toml") || fileExists(root, "requirements.txt") {
		r.Framework = "python"
		return
	}

	if fileExistsAny(root, "vite.config.js", "vite.config.ts", "vite.config.mjs") {
		r.Framework = "vite"
		return
	}

	if fileExists(root, "Cargo.toml") {
		r.Framework = "rust"
		return
	}

	if fileExists(root, "go.mod") {
		r.Framework = "go"
		return
	}

	if fileExists(root, "package.json") {
		r.Framework = "node"
		return
	}
}

func (r *Result) detectDatabase(root string) {
	dbYml := filepath.Join(root, "config", "database.yml")
	if f, err := os.Open(dbYml); err == nil {
		defer func() { _ = f.Close() }()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "adapter:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "adapter:"))
				switch {
				case strings.Contains(val, "sqlite"):
					r.DBAdapter = "sqlite"
				case strings.Contains(val, "postgresql"), strings.Contains(val, "postgis"):
					r.DBAdapter = "postgresql"
				case strings.Contains(val, "mysql"):
					r.DBAdapter = "mysql"
				}
				return
			}
		}
	}

	if r.Framework == "phoenix" {
		if content, err := os.ReadFile(filepath.Join(root, "mix.exs")); err == nil {
			s := string(content)
			switch {
			case strings.Contains(s, ":ecto_sqlite3"):
				r.DBAdapter = "sqlite"
			case strings.Contains(s, ":postgrex"):
				r.DBAdapter = "postgresql"
			case strings.Contains(s, ":myxql"):
				r.DBAdapter = "mysql"
			}
		}
		return
	}

	if r.HasPrisma {
		r.DBAdapter = "postgresql"
	}
}

func (r *Result) detectRedis(root string) {
	if content, err := os.ReadFile(filepath.Join(root, "Gemfile")); err == nil {
		if strings.Contains(string(content), "redis") {
			r.HasRedis = true
			return
		}
	}

	if content, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		s := string(content)
		if strings.Contains(s, "\"redis\"") || strings.Contains(s, "\"ioredis\"") {
			r.HasRedis = true
		}
	}
}

func (r *Result) detectPackageManager(root string) {
	if r.Framework == "phoenix" {
		r.PackageManager = "mix"
		return
	}
	switch {
	case fileExists(root, "pnpm-lock.yaml"):
		r.PackageManager = "pnpm"
	case fileExists(root, "yarn.lock"):
		r.PackageManager = "yarn"
	case fileExists(root, "package-lock.json") || fileExists(root, "package.json"):
		r.PackageManager = "npm"
	case fileExists(root, "Gemfile.lock") || fileExists(root, "Gemfile"):
		r.PackageManager = "bundle"
	case fileExists(root, "mix.lock") || fileExists(root, "mix.exs"):
		r.PackageManager = "mix"
	case fileExists(root, "Cargo.lock") || fileExists(root, "Cargo.toml"):
		r.PackageManager = "cargo"
	case fileExists(root, "requirements.txt") || fileExists(root, "pyproject.toml"):
		r.PackageManager = "pip"
	}
}

// EnvFileCandidates is the priority-ordered list of env file names to check.
var EnvFileCandidates = []string{
	".env.local",
	".env.development",
	".env.development.local",
	".env",
	".env.example",
}

func (r *Result) detectEnvFile(root string) {
	for _, name := range EnvFileCandidates {
		if fileExists(root, name) {
			r.EnvFiles = append(r.EnvFiles, name)
		}
	}

	if len(r.EnvFiles) > 0 {
		r.HasEnvFile = true
		r.EnvFile = r.EnvFiles[0]
	}
}

// AutoLoadsEnvFile reports whether this framework natively loads .env files
// without the user needing to install a dotenv library.
func (r *Result) AutoLoadsEnvFile() bool {
	switch r.Framework {
	case "nextjs", "vite", "rails":
		return true
	default:
		return r.HasDotenv
	}
}

// DefaultEnvTarget returns the conventional env file name for a framework.
func (r *Result) DefaultEnvTarget() string {
	switch r.Framework {
	case "nextjs", "vite":
		return ".env.local"
	case "rails":
		return ".env"
	default:
		return ".env"
	}
}

func (r *Result) detectDotenv(root string) {
	if content, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		s := string(content)
		if strings.Contains(s, "\"dotenv\"") || strings.Contains(s, "\"dotenv-cli\"") {
			r.HasDotenv = true
			return
		}
	}
	if content, err := os.ReadFile(filepath.Join(root, "Gemfile")); err == nil {
		if strings.Contains(string(content), "dotenv") {
			r.HasDotenv = true
			return
		}
	}
	if content, err := os.ReadFile(filepath.Join(root, "requirements.txt")); err == nil {
		if strings.Contains(string(content), "django-environ") || strings.Contains(string(content), "python-dotenv") {
			r.HasDotenv = true
		}
	}
}

func (r *Result) detectPrisma(root string) {
	r.HasPrisma = fileExists(root, "prisma/schema.prisma")
}

// detectPhoenix returns true if mix.exs declares :phoenix as a dependency.
// Also sets IsUmbrella when an apps/ directory of sub-projects is present.
func (r *Result) detectPhoenix(root string) bool {
	content, err := os.ReadFile(filepath.Join(root, "mix.exs"))
	if err != nil {
		return false
	}
	if !strings.Contains(string(content), ":phoenix") {
		return false
	}
	if dirExists(root, "apps") {
		entries, err := os.ReadDir(filepath.Join(root, "apps"))
		if err == nil {
			for _, e := range entries {
				if e.IsDir() && fileExists(root, "apps", e.Name(), "mix.exs") {
					r.IsUmbrella = true
					break
				}
			}
		}
	}
	return true
}

func (r *Result) detectJSBundler(root string) {
	if content, err := os.ReadFile(filepath.Join(root, "Gemfile")); err == nil {
		s := string(content)
		if strings.Contains(s, "jsbundling-rails") || strings.Contains(s, "cssbundling-rails") {
			r.HasJSBundler = true
			return
		}
	}
	if fileExists(root, "Procfile.dev") {
		if content, err := os.ReadFile(filepath.Join(root, "Procfile.dev")); err == nil {
			lines := 0
			for _, line := range strings.Split(string(content), "\n") {
				if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
					lines++
				}
			}
			if lines > 1 {
				r.HasJSBundler = true
			}
		}
	}
}

func fileExists(root string, rel ...string) bool {
	path := filepath.Join(append([]string{root}, rel...)...)
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func fileExistsAny(root string, names ...string) bool {
	for _, name := range names {
		if fileExists(root, name) {
			return true
		}
	}
	return false
}

func dirExists(root, rel string) bool {
	info, err := os.Stat(filepath.Join(root, rel))
	return err == nil && info.IsDir()
}

// IsServerFramework returns true if the detected framework typically runs a
// development server (Rails, Node, Django, etc.) as opposed to a CLI or library.
func (r *Result) IsServerFramework() bool {
	switch r.Framework {
	case "rails", "nextjs", "vite", "node", "django", "python", "phoenix":
		return true
	case "go", "rust", "unknown":
		return false
	default:
		return false
	}
}
