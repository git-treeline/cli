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

func TestForDetection_Python(t *testing.T) {
	det := &detect.Result{
		Framework:      "python",
		PackageManager: "pip",
		EnvFile:        ".env",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "pip install")
}

func TestForDetection_Generic(t *testing.T) {
	det := &detect.Result{
		Framework: "unknown",
		EnvFile:   ".env",
	}
	content := ForDetection("myapp", "", det)

	assertValidYAML(t, content)
	assertContains(t, content, "project: myapp")
	assertContains(t, content, `PORT: "{port}"`)
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
