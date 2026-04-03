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
	assertContains(t, content, "npm install")
	assertContains(t, content, ".env.local")
	assertNotContains(t, content, "bundle install")
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
		DBAdapter:      "postgresql",
		HasRedis:       true,
		PackageManager: "bundle",
		EnvFile:        ".env.local",
	}
	content := ForDetection("myapp", "myapp_dev", det)

	assertValidYAML(t, content)
	assertContains(t, content, "adapter: postgresql")
	assertContains(t, content, "bundle install")
	assertContains(t, content, `REDIS_URL: "{redis_url}"`)
	assertContains(t, content, "ports_needed: 2")
	assertContains(t, content, "config/master.key")
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
	assertContains(t, content, "npm install")
	assertNotContains(t, content, "database")
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
	assertContains(t, content, "pip install")
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

func TestForDetection_DefaultBranch_NonMain(t *testing.T) {
	det := &detect.Result{
		Framework:     "node",
		HasEnvFile:    true,
		PackageManager: "npm",
		EnvFile:       ".env",
		DefaultBranch: "develop",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "default_branch: develop")
}

func TestForDetection_DefaultBranch_Main_Omitted(t *testing.T) {
	det := &detect.Result{
		Framework:     "node",
		HasEnvFile:    true,
		PackageManager: "npm",
		EnvFile:       ".env",
		DefaultBranch: "main",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertNotContains(t, content, "default_branch")
}

func TestForDetection_DefaultBranch_Empty_Omitted(t *testing.T) {
	det := &detect.Result{
		Framework:     "node",
		HasEnvFile:    true,
		PackageManager: "npm",
		EnvFile:       ".env",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertNotContains(t, content, "default_branch")
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
	assertContains(t, content, "source: .env.development")
	assertContains(t, content, "target: .env.development")
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
	assertContains(t, content, "source: .env.development")
	assertContains(t, content, "target: .env.development")
}

func TestPortHint_NextJS(t *testing.T) {
	det := &detect.Result{Framework: "nextjs"}
	hint := PortHint(det)
	if !strings.Contains(hint, "next dev --port") {
		t.Errorf("expected Next.js port hint, got: %s", hint)
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
