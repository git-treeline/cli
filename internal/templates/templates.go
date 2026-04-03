// Package templates provides YAML and agent context generation.
// It generates framework-specific .treeline.yml configurations based
// on detection results, and optionally creates Cursor rules or
// CLAUDE.md sections for AI coding assistants.
package templates

import (
	"fmt"
	"strings"

	"github.com/git-treeline/git-treeline/internal/detect"
)

// ForDetection returns a .treeline.yml template based on the detection result.
func ForDetection(project, templateDB string, det *detect.Result) string {
	switch det.Framework {
	case "nextjs":
		return nextJS(project, templateDB, det)
	case "rails":
		return rails(project, templateDB, det)
	case "node":
		return node(project, det)
	case "django", "python":
		return python(project, det)
	default:
		return generic(project, det)
	}
}

func nextJS(project, templateDB string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeDefaultBranch(&b, det)

	if det.HasEnvFile {
		writeEnvFileBlock(&b, det.EnvFile)
	}

	envVars := map[string]string{
		"PORT":                `"{port}"`,
		"NEXT_PUBLIC_APP_URL": `"http://localhost:{port}"`,
	}

	if det.HasPrisma && det.DBAdapter == "postgresql" {
		fmt.Fprintf(&b, "\ndatabase:\n")
		fmt.Fprintf(&b, "  adapter: postgresql\n")
		fmt.Fprintf(&b, "  template: %s\n", templateDB)
		fmt.Fprintf(&b, "  pattern: \"{template}_{worktree}\"\n")
		envVars["DATABASE_URL"] = `"postgresql://localhost:5432/{database}"`
	}

	if det.HasEnvFile {
		b.WriteString("\nenv:\n")
		for k, v := range envVars {
			fmt.Fprintf(&b, "  %s: %s\n", k, v)
		}
	}

	b.WriteString("\nsetup_commands:\n")
	fmt.Fprintf(&b, "  - %s\n", installCmd(det))
	if det.HasPrisma {
		b.WriteString("  - npx prisma migrate deploy\n")
	}

	return b.String()
}

func rails(project, templateDB string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeDefaultBranch(&b, det)
	b.WriteString("ports_needed: 2\n")

	if det.HasEnvFile {
		writeEnvFileBlock(&b, det.EnvFile)
	}

	adapter := det.DBAdapter
	if adapter == "" {
		adapter = "postgresql"
	}

	fmt.Fprintf(&b, "\ndatabase:\n")
	fmt.Fprintf(&b, "  adapter: %s\n", adapter)
	if adapter == "sqlite" {
		fmt.Fprintf(&b, "  template: db/development.sqlite3\n")
		fmt.Fprintf(&b, "  pattern: \"db/{worktree}.sqlite3\"\n")
	} else {
		fmt.Fprintf(&b, "  template: %s\n", templateDB)
		fmt.Fprintf(&b, "  pattern: \"{template}_{worktree}\"\n")
	}

	b.WriteString("\ncopy_files:\n")
	b.WriteString("  - config/master.key\n")

	if det.HasEnvFile {
		b.WriteString("\nenv:\n")
		fmt.Fprintf(&b, "  PORT: \"{port}\"\n")
		if adapter == "sqlite" {
			fmt.Fprintf(&b, "  DATABASE_PATH: \"{database}\"\n")
		} else {
			fmt.Fprintf(&b, "  DATABASE_NAME: \"{database}\"\n")
		}
		if det.HasRedis {
			fmt.Fprintf(&b, "  REDIS_URL: \"{redis_url}\"\n")
		}
		fmt.Fprintf(&b, "  ESBUILD_PORT: \"{port_2}\"\n")
		fmt.Fprintf(&b, "  APPLICATION_HOST: \"localhost:{port}\"\n")
	}

	b.WriteString("\nsetup_commands:\n")
	b.WriteString("  - bundle install --quiet\n")
	b.WriteString("  # - yarn install --silent\n")

	b.WriteString("\neditor:\n")
	b.WriteString("  vscode_title: '{project} (:{port}) — {branch} — ${activeEditorShort}'\n")

	return b.String()
}

func node(project string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeDefaultBranch(&b, det)

	if det.HasEnvFile {
		writeEnvFileBlock(&b, det.EnvFile)
		b.WriteString("\nenv:\n")
		b.WriteString("  PORT: \"{port}\"\n")
	}

	b.WriteString("\nsetup_commands:\n")
	fmt.Fprintf(&b, "  - %s\n", installCmd(det))
	return b.String()
}

func python(project string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeDefaultBranch(&b, det)

	if det.HasEnvFile {
		writeEnvFileBlock(&b, det.EnvFile)
		b.WriteString("\nenv:\n")
		b.WriteString("  PORT: \"{port}\"\n")
	}

	b.WriteString("\nsetup_commands:\n")
	b.WriteString("  - pip install -r requirements.txt\n")
	return b.String()
}

func generic(project string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeDefaultBranch(&b, det)

	if det.HasEnvFile {
		writeEnvFileBlock(&b, det.EnvFile)
		b.WriteString("\nenv:\n")
		b.WriteString("  PORT: \"{port}\"\n")
	}

	return b.String()
}

func writeEnvFileBlock(b *strings.Builder, envFile string) {
	b.WriteString("\nenv_file:\n")
	fmt.Fprintf(b, "  target: %s\n", envFile)
	fmt.Fprintf(b, "  source: %s\n", envFile)
}

func writeDefaultBranch(b *strings.Builder, det *detect.Result) {
	if det.DefaultBranch != "" && det.DefaultBranch != "main" {
		fmt.Fprintf(b, "default_branch: %s\n", det.DefaultBranch)
	}
}

// PortHint returns framework-specific guidance on wiring the allocated PORT
// into the dev server. Returns empty string if no hint is needed.
func PortHint(det *detect.Result) string {
	switch det.Framework {
	case "nextjs":
		envFile := det.EnvFile
		if envFile == "" {
			envFile = ".env.local"
		}
		return fmt.Sprintf(`Next.js does not read PORT from %s for the dev server.
Update your package.json dev script:

  "dev": "next dev --port ${PORT:-3000}"

Or use dotenv-cli to load %s before starting:

  npm install -D dotenv-cli
  "dev": "dotenv -e %s -- next dev --port $PORT"`, envFile, envFile, envFile)
	case "node":
		return `Ensure your server reads the allocated port from the environment:

  const port = process.env.PORT || 3000;
  app.listen(port);`
	case "django", "python":
		return `Pass the allocated port to your dev server:

  python manage.py runserver 0.0.0.0:${PORT:-8000}

Or in your WSGI/ASGI config, read os.environ["PORT"].`
	default:
		return ""
	}
}

func installCmd(det *detect.Result) string {
	switch det.PackageManager {
	case "yarn":
		return "yarn install --silent"
	case "pnpm":
		return "pnpm install --silent"
	default:
		return "npm install"
	}
}
