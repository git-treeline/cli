package templates

import (
	"strings"
	"testing"

	"github.com/git-treeline/git-treeline/internal/detect"
	"gopkg.in/yaml.v3"
)

func TestForDetection_NextJS(t *testing.T) {
	det := &detect.Result{
		Framework:      "nextjs",
		HasEnvFile:     true,
		PackageManager: "npm",
		EnvFile:        ".env.local",
	}
	content := ForDetection("myapp", "myapp_dev", det)

	assertValidYAML(t, content)
	assertContains(t, content, "project: myapp")
	assertContains(t, content, `PORT: "{port}"`)
	assertContains(t, content, "commands:")
	assertContains(t, content, "npm install")
	assertContains(t, content, "start: npm run dev")
	assertContains(t, content, ".env.local")
	assertNotContains(t, content, "bundle install")
	assertNotContains(t, content, "setup_commands")
}

func TestForDetection_NextJS_Prisma(t *testing.T) {
	det := &detect.Result{
		Framework:      "nextjs",
		HasPrisma:      true,
		HasEnvFile:     true,
		DBAdapter:      "postgresql",
		PackageManager: "yarn",
		EnvFile:        ".env.local",
	}
	content := ForDetection("myapp", "myapp_dev", det)

	assertValidYAML(t, content)
	assertContains(t, content, "adapter: postgresql")
	assertContains(t, content, "DATABASE_URL")
	assertContains(t, content, "prisma migrate deploy")
	assertContains(t, content, "yarn install")
}

func TestForDetection_Rails_PostgreSQL(t *testing.T) {
	det := &detect.Result{
		Framework:      "rails",
		HasEnvFile:     true,
		HasJSBundler:   true,
		DBAdapter:      "postgresql",
		HasRedis:       true,
		PackageManager: "bundle",
		EnvFile:        ".env.local",
	}
	content := ForDetection("myapp", "myapp_dev", det)

	assertValidYAML(t, content)
	assertContains(t, content, "adapter: postgresql")
	assertContains(t, content, "commands:")
	assertContains(t, content, "bundle install")
	assertContains(t, content, "start: bin/dev")
	assertContains(t, content, `REDIS_URL: "{redis_url}"`)
	assertContains(t, content, "port_count: 2")
	assertContains(t, content, `ESBUILD_PORT: "{port_2}"`)
	assertContains(t, content, "yarn install")
	assertContains(t, content, "config/master.key")
	assertNotContains(t, content, "setup_commands")
}

func TestForDetection_Rails_NoBundler(t *testing.T) {
	det := &detect.Result{
		Framework:      "rails",
		HasEnvFile:     true,
		HasJSBundler:   false,
		DBAdapter:      "postgresql",
		PackageManager: "bundle",
		EnvFile:        ".env.local",
	}
	content := ForDetection("myapp", "myapp_dev", det)

	assertValidYAML(t, content)
	assertNotContains(t, content, "port_count")
	assertNotContains(t, content, "ESBUILD_PORT")
	assertNotContains(t, content, "yarn install")
}

func TestForDetection_Rails_SQLite(t *testing.T) {
	det := &detect.Result{
		Framework:      "rails",
		HasEnvFile:     true,
		DBAdapter:      "sqlite",
		PackageManager: "bundle",
		EnvFile:        ".env.local",
	}
	content := ForDetection("myapp", "myapp_dev", det)

	assertValidYAML(t, content)
	assertContains(t, content, "adapter: sqlite")
	assertContains(t, content, "development.sqlite3")
	assertContains(t, content, "DATABASE_PATH")
	assertNotContains(t, content, "DATABASE_NAME")
}

func TestForDetection_Node(t *testing.T) {
	det := &detect.Result{
		Framework:      "node",
		HasEnvFile:     true,
		PackageManager: "npm",
		EnvFile:        ".env",
	}
	content := ForDetection("myapi", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "project: myapi")
	assertContains(t, content, `PORT: "{port}"`)
	assertContains(t, content, "commands:")
	assertContains(t, content, "npm install")
	assertContains(t, content, "start: npm run dev")
	assertNotContains(t, content, "database")
	assertNotContains(t, content, "setup_commands")
}

func TestForDetection_Node_NoEnvFile(t *testing.T) {
	det := &detect.Result{
		Framework:      "node",
		HasEnvFile:     false,
		PackageManager: "npm",
		EnvFile:        ".env",
	}
	content := ForDetection("website", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "project: website")
	assertContains(t, content, "npm install")
	assertNotContains(t, content, "env_file")
	assertNotContains(t, content, "PORT")
}

func TestForDetection_Python(t *testing.T) {
	det := &detect.Result{
		Framework:      "python",
		HasEnvFile:     true,
		PackageManager: "pip",
		EnvFile:        ".env",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "commands:")
	assertContains(t, content, "pip install")
	assertContains(t, content, "start: python manage.py runserver")
	assertNotContains(t, content, "setup_commands")
}

func TestForDetection_Generic(t *testing.T) {
	det := &detect.Result{
		Framework:  "unknown",
		HasEnvFile: true,
		EnvFile:    ".env",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "project: myapp")
	assertContains(t, content, `PORT: "{port}"`)
}

func TestForDetection_Generic_NoEnvFile(t *testing.T) {
	det := &detect.Result{
		Framework:  "unknown",
		HasEnvFile: false,
		EnvFile:    ".env",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "project: myapp")
	assertNotContains(t, content, "env_file")
	assertNotContains(t, content, "PORT")
}

func TestForDetection_MergeTarget_NonMain(t *testing.T) {
	det := &detect.Result{
		Framework:      "node",
		HasEnvFile:     true,
		PackageManager: "npm",
		EnvFile:        ".env",
		MergeTarget:    "develop",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "merge_target: develop")
}

func TestForDetection_MergeTarget_Main_Omitted(t *testing.T) {
	det := &detect.Result{
		Framework:      "node",
		HasEnvFile:     true,
		PackageManager: "npm",
		EnvFile:        ".env",
		MergeTarget:    "main",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertNotContains(t, content, "merge_target")
}

func TestForDetection_MergeTarget_Empty_Omitted(t *testing.T) {
	det := &detect.Result{
		Framework:      "node",
		HasEnvFile:     true,
		PackageManager: "npm",
		EnvFile:        ".env",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertNotContains(t, content, "merge_target")
}

func TestForDetection_Rails_EnvDevelopment(t *testing.T) {
	det := &detect.Result{
		Framework:      "rails",
		HasEnvFile:     true,
		DBAdapter:      "postgresql",
		PackageManager: "bundle",
		EnvFile:        ".env.development",
	}
	content := ForDetection("myapp", "myapp_dev", det)

	assertValidYAML(t, content)
	assertContains(t, content, "env_file: .env.development")
	assertNotContains(t, content, ".env.local")
}

func TestForDetection_NextJS_EnvDevelopment(t *testing.T) {
	det := &detect.Result{
		Framework:      "nextjs",
		HasEnvFile:     true,
		PackageManager: "npm",
		EnvFile:        ".env.development",
	}
	content := ForDetection("myapp", "myapp_dev", det)

	assertValidYAML(t, content)
	assertContains(t, content, "env_file: .env.development")
}

func TestForDetection_Vite(t *testing.T) {
	det := &detect.Result{
		Framework:      "vite",
		PackageManager: "npm",
	}
	content := ForDetection("website", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "project: website")
	assertContains(t, content, "env_file: .env.local")
	assertContains(t, content, `PORT: "{port}"`)
	assertContains(t, content, "commands:")
	assertContains(t, content, "npm install")
	assertContains(t, content, "start: npx vite --port {port} --host")
	assertNotContains(t, content, "setup_commands")
}

func TestForDetection_Vite_NoEnvFile_StillEmitsEnv(t *testing.T) {
	det := &detect.Result{
		Framework:      "vite",
		HasEnvFile:     false,
		PackageManager: "npm",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "env_file: .env.local")
	assertContains(t, content, `PORT: "{port}"`)
}

func TestForDetection_NextJS_NoEnvFile_StillEmitsEnv(t *testing.T) {
	det := &detect.Result{
		Framework:      "nextjs",
		HasEnvFile:     false,
		PackageManager: "npm",
	}
	content := ForDetection("myapp", "myapp_dev", det)

	assertValidYAML(t, content)
	assertContains(t, content, "env_file: .env.local")
}

func TestForDetection_Node_NoEnvFile_NoDotenv_NoEnvBlock(t *testing.T) {
	det := &detect.Result{
		Framework:      "node",
		HasEnvFile:     false,
		PackageManager: "npm",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertNotContains(t, content, "env_file")
}

func TestForDetection_Node_NoDotenv_WithEnvFile_EmitsEnv(t *testing.T) {
	det := &detect.Result{
		Framework:      "node",
		HasEnvFile:     true,
		EnvFile:        ".env",
		PackageManager: "npm",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "env_file: .env")
}

func TestDiagnose_Vite(t *testing.T) {
	det := &detect.Result{Framework: "vite"}
	diags := Diagnose(det)

	hasPortWarning := false
	hasEnvInfo := false
	for _, d := range diags {
		if strings.Contains(d.Message, "{port}") {
			hasPortWarning = true
		}
		if strings.Contains(d.Message, "auto-loads") {
			hasEnvInfo = true
		}
	}
	if !hasPortWarning {
		t.Error("expected port wiring diagnostic for Vite")
	}
	if !hasEnvInfo {
		t.Error("expected env auto-load info for Vite")
	}
}

func TestDiagnose_Node_NoDotenv(t *testing.T) {
	det := &detect.Result{Framework: "node", HasDotenv: false}
	diags := Diagnose(det)

	hasDotenvWarning := false
	for _, d := range diags {
		if d.Level == "warn" && strings.Contains(d.Message, "dotenv") {
			hasDotenvWarning = true
		}
	}
	if !hasDotenvWarning {
		t.Error("expected dotenv warning for Node without dotenv")
	}
}

func TestDiagnose_Rails_NoWarnings(t *testing.T) {
	det := &detect.Result{Framework: "rails", HasEnvFile: true, EnvFile: ".env"}
	diags := Diagnose(det)

	for _, d := range diags {
		if d.Level == "warn" {
			t.Errorf("unexpected warning for Rails: %s", d.Message)
		}
	}
}

func TestPortHint_Vite(t *testing.T) {
	det := &detect.Result{Framework: "vite"}
	hint := PortHint(det)
	if !strings.Contains(hint, "{port}") {
		t.Errorf("expected Vite port hint to mention {port}, got: %s", hint)
	}
	if !strings.Contains(hint, "--port") {
		t.Errorf("expected --port flag in hint, got: %s", hint)
	}
}

func TestPortHint_NextJS(t *testing.T) {
	det := &detect.Result{Framework: "nextjs"}
	hint := PortHint(det)
	if !strings.Contains(hint, "--port {port}") {
		t.Errorf("expected Next.js port hint with {port}, got: %s", hint)
	}
}

func TestPortHint_Node(t *testing.T) {
	det := &detect.Result{Framework: "node"}
	hint := PortHint(det)
	if !strings.Contains(hint, "process.env.PORT") {
		t.Errorf("expected Node port hint, got: %s", hint)
	}
}

func TestPortHint_Rails(t *testing.T) {
	det := &detect.Result{Framework: "rails"}
	hint := PortHint(det)
	if hint != "" {
		t.Errorf("expected no hint for Rails, got: %s", hint)
	}
}

func TestPortHint_Python(t *testing.T) {
	det := &detect.Result{Framework: "django"}
	hint := PortHint(det)
	if !strings.Contains(hint, "manage.py runserver") {
		t.Errorf("expected Django port hint, got: %s", hint)
	}
}

func assertValidYAML(t *testing.T, content string) {
	t.Helper()
	var data map[string]any
	if err := yaml.Unmarshal([]byte(content), &data); err != nil {
		t.Errorf("invalid YAML:\n%s\nerror: %v", content, err)
	}
}

func assertContains(t *testing.T, content, substr string) {
	t.Helper()
	if !strings.Contains(content, substr) {
		t.Errorf("expected content to contain %q, got:\n%s", substr, content)
	}
}

func assertNotContains(t *testing.T, content, substr string) {
	t.Helper()
	if strings.Contains(content, substr) {
		t.Errorf("expected content to NOT contain %q, got:\n%s", substr, content)
	}
}
