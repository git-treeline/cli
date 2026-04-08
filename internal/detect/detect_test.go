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
	if r.EnvFile != "" {
		t.Errorf("expected empty EnvFile when no env file exists, got %s", r.EnvFile)
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

func TestDetect_Node_EnvDevelopment(t *testing.T) {
	dir := setup(t, "package.json", ".env.development")
	r := Detect(dir)
	if !r.HasEnvFile {
		t.Error("expected HasEnvFile=true")
	}
	if r.EnvFile != ".env.development" {
		t.Errorf("expected .env.development, got %s", r.EnvFile)
	}
}

func TestDetect_MultipleEnvFiles(t *testing.T) {
	dir := setup(t, "package.json", ".env.development", ".env", ".env.example")
	r := Detect(dir)
	if r.EnvFile != ".env.development" {
		t.Errorf("expected .env.development as primary, got %s", r.EnvFile)
	}
	if len(r.EnvFiles) != 3 {
		t.Errorf("expected 3 env files, got %d: %v", len(r.EnvFiles), r.EnvFiles)
	}
}

func TestDetect_EnvLocalTakesPriority(t *testing.T) {
	dir := setup(t, "Gemfile", "config/application.rb", ".env.local", ".env.development")
	r := Detect(dir)
	if r.EnvFile != ".env.local" {
		t.Errorf("expected .env.local (higher priority), got %s", r.EnvFile)
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

func TestDetect_Vite(t *testing.T) {
	dir := setup(t, "package.json", "vite.config.js")
	r := Detect(dir)
	if r.Framework != "vite" {
		t.Errorf("expected vite, got %s", r.Framework)
	}
	if !r.AutoLoadsEnvFile() {
		t.Error("expected Vite to auto-load env files")
	}
	if r.DefaultEnvTarget() != ".env.local" {
		t.Errorf("expected .env.local default, got %s", r.DefaultEnvTarget())
	}
}

func TestDetect_Vite_TS(t *testing.T) {
	dir := setup(t, "package.json", "vite.config.ts")
	r := Detect(dir)
	if r.Framework != "vite" {
		t.Errorf("expected vite, got %s", r.Framework)
	}
}

func TestDetect_Vite_NoEnvFile(t *testing.T) {
	dir := setup(t, "package.json", "vite.config.js")
	r := Detect(dir)
	if r.HasEnvFile {
		t.Error("expected HasEnvFile=false")
	}
	if !r.AutoLoadsEnvFile() {
		t.Error("Vite should auto-load env even without existing file")
	}
}

func TestDetect_NextJS_NotVite(t *testing.T) {
	dir := setup(t, "package.json", "next.config.js", "vite.config.js")
	r := Detect(dir)
	if r.Framework != "nextjs" {
		t.Errorf("expected nextjs (more specific), got %s", r.Framework)
	}
}

func TestDetect_Dotenv_Node(t *testing.T) {
	dir := setup(t, "package.json")
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"dotenv":"^16.0.0"}}`), 0o644)
	r := Detect(dir)
	if !r.HasDotenv {
		t.Error("expected HasDotenv=true with dotenv in dependencies")
	}
}

func TestDetect_Dotenv_Python(t *testing.T) {
	dir := setup(t, "requirements.txt")
	_ = os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("django\npython-dotenv\n"), 0o644)
	r := Detect(dir)
	if !r.HasDotenv {
		t.Error("expected HasDotenv=true with python-dotenv")
	}
}

func TestDetect_AutoLoadsEnvFile(t *testing.T) {
	cases := []struct {
		framework string
		files     []string
		expected  bool
	}{
		{"nextjs", []string{"package.json", "next.config.js"}, true},
		{"vite", []string{"package.json", "vite.config.js"}, true},
		{"rails", []string{"Gemfile", "config/application.rb"}, true},
		{"node", []string{"package.json"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.framework, func(t *testing.T) {
			dir := setup(t, tc.files...)
			r := Detect(dir)
			if r.AutoLoadsEnvFile() != tc.expected {
				t.Errorf("expected AutoLoadsEnvFile=%v for %s", tc.expected, tc.framework)
			}
		})
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

func TestDetect_JSBundler_Gemfile(t *testing.T) {
	dir := setup(t, "Gemfile", "config/application.rb", "config/database.yml")
	_ = os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("gem 'rails'\ngem 'jsbundling-rails'\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "config/database.yml"), []byte("development:\n  adapter: postgresql\n"), 0o644)
	r := Detect(dir)
	if !r.HasJSBundler {
		t.Error("expected HasJSBundler=true with jsbundling-rails")
	}
}

func TestDetect_JSBundler_CSSBundling(t *testing.T) {
	dir := setup(t, "Gemfile", "config/application.rb")
	_ = os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("gem 'rails'\ngem 'cssbundling-rails'\n"), 0o644)
	r := Detect(dir)
	if !r.HasJSBundler {
		t.Error("expected HasJSBundler=true with cssbundling-rails")
	}
}

func TestDetect_JSBundler_ProcfileDev(t *testing.T) {
	dir := setup(t, "Gemfile", "config/application.rb", "Procfile.dev")
	_ = os.WriteFile(filepath.Join(dir, "Procfile.dev"), []byte("web: bin/rails server\njs: yarn build --watch\n"), 0o644)
	r := Detect(dir)
	if !r.HasJSBundler {
		t.Error("expected HasJSBundler=true with multi-process Procfile.dev")
	}
}

func TestDetect_NoJSBundler(t *testing.T) {
	dir := setup(t, "Gemfile", "config/application.rb")
	_ = os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("gem 'rails'\ngem 'importmap-rails'\n"), 0o644)
	r := Detect(dir)
	if r.HasJSBundler {
		t.Error("expected HasJSBundler=false with importmap-rails")
	}
}

func TestIsServerFramework(t *testing.T) {
	cases := []struct {
		framework string
		expected  bool
	}{
		{"rails", true},
		{"nextjs", true},
		{"vite", true},
		{"node", true},
		{"django", true},
		{"python", true},
		{"go", false},
		{"rust", false},
		{"unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.framework, func(t *testing.T) {
			r := &Result{Framework: tc.framework}
			if got := r.IsServerFramework(); got != tc.expected {
				t.Errorf("IsServerFramework()=%v for %s, want %v", got, tc.framework, tc.expected)
			}
		})
	}
}
