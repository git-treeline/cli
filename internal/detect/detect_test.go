package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		path := filepath.Join(dir, f)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(""), 0o644)
	}
	return dir
}

func TestDetect_NextJS(t *testing.T) {
	dir := setup(t, "package.json", "next.config.mjs", ".env.local")
	r := Detect(dir)
	if r.Framework != "nextjs" {
		t.Errorf("expected nextjs, got %s", r.Framework)
	}
	if r.EnvFile != ".env.local" {
		t.Errorf("expected .env.local, got %s", r.EnvFile)
	}
	if !r.HasEnvFile {
		t.Error("expected HasEnvFile=true")
	}
	if r.PackageManager != "npm" {
		t.Errorf("expected npm, got %s", r.PackageManager)
	}
}

func TestDetect_NextJS_WithYarn(t *testing.T) {
	dir := setup(t, "package.json", "next.config.js", "yarn.lock")
	r := Detect(dir)
	if r.Framework != "nextjs" {
		t.Errorf("expected nextjs, got %s", r.Framework)
	}
	if r.PackageManager != "yarn" {
		t.Errorf("expected yarn, got %s", r.PackageManager)
	}
}

func TestDetect_NextJS_WithPrisma(t *testing.T) {
	dir := setup(t, "package.json", "next.config.ts", "prisma/schema.prisma")
	r := Detect(dir)
	if r.Framework != "nextjs" {
		t.Errorf("expected nextjs, got %s", r.Framework)
	}
	if !r.HasPrisma {
		t.Error("expected HasPrisma=true")
	}
	if r.DBAdapter != "postgresql" {
		t.Errorf("expected postgresql from prisma detection, got %s", r.DBAdapter)
	}
}

func TestDetect_Rails_PostgreSQL(t *testing.T) {
	dir := setup(t, "Gemfile", "config/application.rb", "config/database.yml")
	_ = os.WriteFile(filepath.Join(dir, "config/database.yml"), []byte("development:\n  adapter: postgresql\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("gem 'rails'\ngem 'redis'\n"), 0o644)

	r := Detect(dir)
	if r.Framework != "rails" {
		t.Errorf("expected rails, got %s", r.Framework)
	}
	if r.DBAdapter != "postgresql" {
		t.Errorf("expected postgresql, got %s", r.DBAdapter)
	}
	if !r.HasRedis {
		t.Error("expected HasRedis=true")
	}
	if r.PackageManager != "bundle" {
		t.Errorf("expected bundle, got %s", r.PackageManager)
	}
}

func TestDetect_Rails_SQLite(t *testing.T) {
	dir := setup(t, "Gemfile", "config/application.rb", "config/database.yml")
	_ = os.WriteFile(filepath.Join(dir, "config/database.yml"), []byte("development:\n  adapter: sqlite3\n"), 0o644)

	r := Detect(dir)
	if r.Framework != "rails" {
		t.Errorf("expected rails, got %s", r.Framework)
	}
	if r.DBAdapter != "sqlite" {
		t.Errorf("expected sqlite, got %s", r.DBAdapter)
	}
}

func TestDetect_Node(t *testing.T) {
	dir := setup(t, "package.json")
	r := Detect(dir)
	if r.Framework != "node" {
		t.Errorf("expected node, got %s", r.Framework)
	}
	if r.EnvFile != ".env" {
		t.Errorf("expected .env, got %s", r.EnvFile)
	}
	if r.HasEnvFile {
		t.Error("expected HasEnvFile=false when no .env file exists")
	}
}

func TestDetect_Node_WithEnvFile(t *testing.T) {
	dir := setup(t, "package.json", ".env")
	r := Detect(dir)
	if r.Framework != "node" {
		t.Errorf("expected node, got %s", r.Framework)
	}
	if !r.HasEnvFile {
		t.Error("expected HasEnvFile=true when .env exists")
	}
}

func TestDetect_HasEnvFile_Example(t *testing.T) {
	dir := setup(t, "package.json", ".env.example")
	r := Detect(dir)
	if !r.HasEnvFile {
		t.Error("expected HasEnvFile=true when .env.example exists")
	}
}

func TestDetect_Python(t *testing.T) {
	dir := setup(t, "requirements.txt")
	r := Detect(dir)
	if r.Framework != "python" {
		t.Errorf("expected python, got %s", r.Framework)
	}
	if r.PackageManager != "pip" {
		t.Errorf("expected pip, got %s", r.PackageManager)
	}
}

func TestDetect_Django(t *testing.T) {
	dir := setup(t, "manage.py", "requirements.txt")
	r := Detect(dir)
	if r.Framework != "django" {
		t.Errorf("expected django, got %s", r.Framework)
	}
}

func TestDetect_Rust(t *testing.T) {
	dir := setup(t, "Cargo.toml")
	r := Detect(dir)
	if r.Framework != "rust" {
		t.Errorf("expected rust, got %s", r.Framework)
	}
	if r.PackageManager != "cargo" {
		t.Errorf("expected cargo, got %s", r.PackageManager)
	}
}

func TestDetect_Go(t *testing.T) {
	dir := setup(t, "go.mod")
	r := Detect(dir)
	if r.Framework != "go" {
		t.Errorf("expected go, got %s", r.Framework)
	}
}

func TestDetect_Unknown(t *testing.T) {
	dir := t.TempDir()
	r := Detect(dir)
	if r.Framework != "unknown" {
		t.Errorf("expected unknown, got %s", r.Framework)
	}
}

func TestDetect_Pnpm(t *testing.T) {
	dir := setup(t, "package.json", "pnpm-lock.yaml", "next.config.js")
	r := Detect(dir)
	if r.PackageManager != "pnpm" {
		t.Errorf("expected pnpm, got %s", r.PackageManager)
	}
}
