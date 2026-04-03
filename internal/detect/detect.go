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
	Framework      string   // "nextjs", "rails", "node", "django", "python", "rust", "go", "unknown"
	HasPrisma      bool
	DBAdapter      string   // "postgresql", "sqlite", ""
	HasRedis       bool
	HasEnvFile     bool     // true if any env file exists on disk
	EnvFile        string   // best candidate: ".env.local", ".env.development", ".env", etc.
	EnvFiles       []string // all env files found, in priority order
	PackageManager string   // "npm", "yarn", "pnpm", "bundle", "cargo", "pip", ""
	DefaultBranch  string   // set by caller when git context is available
}

func Detect(root string) *Result {
	r := &Result{Framework: "unknown"}

	r.detectFramework(root)
	r.detectPrisma(root)
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

	if fileExists(root, "manage.py") || (fileExists(root, "pyproject.toml") && dirExists(root, "templates")) {
		r.Framework = "django"
		return
	}

	if fileExists(root, "pyproject.toml") || fileExists(root, "requirements.txt") {
		r.Framework = "python"
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

	if r.HasPrisma {
		r.DBAdapter = "postgresql"
	}
}

func (r *Result) detectRedis(root string) {
	// Check Gemfile for redis
	if content, err := os.ReadFile(filepath.Join(root, "Gemfile")); err == nil {
		if strings.Contains(string(content), "redis") {
			r.HasRedis = true
			return
		}
	}
	// Check package.json for redis/ioredis
	if content, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		s := string(content)
		if strings.Contains(s, "\"redis\"") || strings.Contains(s, "\"ioredis\"") {
			r.HasRedis = true
		}
	}
}

func (r *Result) detectPackageManager(root string) {
	switch {
	case fileExists(root, "pnpm-lock.yaml"):
		r.PackageManager = "pnpm"
	case fileExists(root, "yarn.lock"):
		r.PackageManager = "yarn"
	case fileExists(root, "package-lock.json") || fileExists(root, "package.json"):
		r.PackageManager = "npm"
	case fileExists(root, "Gemfile.lock") || fileExists(root, "Gemfile"):
		r.PackageManager = "bundle"
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

func (r *Result) detectPrisma(root string) {
	r.HasPrisma = fileExists(root, "prisma/schema.prisma")
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
