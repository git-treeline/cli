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
	case "vite":
		return vite(project, det)
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
	writeMergeTarget(&b, det)

	if !det.HasPrisma || det.DBAdapter != "postgresql" {
		b.WriteString("\n# database:\n")
		b.WriteString("#   adapter: postgresql\n")
		fmt.Fprintf(&b, "#   template: %s\n", templateDB)
		b.WriteString("#   pattern: \"{template}_{worktree}\"\n")
	}

	emit := shouldEmitEnv(det)
	if emit {
		writeEnvFileBlock(&b, envTarget(det))
	}

	if det.HasPrisma && det.DBAdapter == "postgresql" {
		fmt.Fprintf(&b, "\ndatabase:\n")
		fmt.Fprintf(&b, "  adapter: postgresql\n")
		fmt.Fprintf(&b, "  template: %s\n", templateDB)
		fmt.Fprintf(&b, "  pattern: \"{template}_{worktree}\"\n")
	}

	if emit {
		b.WriteString("\nenv:\n")
		fmt.Fprintf(&b, "  PORT: \"{port}\"\n")
		if det.HasPrisma && det.DBAdapter == "postgresql" {
			fmt.Fprintf(&b, "  DATABASE_URL: \"postgresql://localhost:5432/{database}\"\n")
		}
		b.WriteString(`  # NEXT_PUBLIC_APP_URL: "http://localhost:{port}"` + "\n")
		writeResolveComment(&b)
	}

	b.WriteString("\ncommands:\n")
	b.WriteString("  setup:\n")
	fmt.Fprintf(&b, "    - %s\n", installCmd(det))
	if det.HasPrisma {
		b.WriteString("    - npx prisma migrate deploy\n")
	}
	writeStartCommand(&b, det)

	writeHooksComment(&b)
	writeEditorComment(&b)
	return b.String()
}

func rails(project, templateDB string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeMergeTarget(&b, det)
	if det.HasJSBundler {
		b.WriteString("# port_count: 2                    # uncomment if JS bundler needs its own port\n")
	}

	emit := shouldEmitEnv(det)
	if emit {
		writeEnvFileBlock(&b, envTarget(det))
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

	if emit {
		b.WriteString("\nenv:\n")
		fmt.Fprintf(&b, "  PORT: \"{port}\"\n")
		if adapter == "sqlite" {
			fmt.Fprintf(&b, "  DATABASE_PATH: \"{database}\"\n")
		} else {
			fmt.Fprintf(&b, "  DATABASE_NAME: \"{database}\"\n")
		}
		if det.HasRedis {
			fmt.Fprintf(&b, "  REDIS_URL: \"{redis_url}\"\n")
		} else {
			b.WriteString(`  # REDIS_URL: "{redis_url}"` + "\n")
		}
		if det.HasJSBundler {
			b.WriteString(`  # ESBUILD_PORT: "{port_2}"       # requires port_count: 2` + "\n")
		}
		b.WriteString(`  # APPLICATION_HOST: "localhost:{port}"` + "\n")
		writeResolveComment(&b)
	}

	b.WriteString("\ncommands:\n")
	b.WriteString("  setup:\n")
	b.WriteString("    - bundle install --quiet\n")
	if det.HasJSBundler {
		b.WriteString("    - yarn install --silent\n")
	}
	b.WriteString("  start: bin/dev\n")

	if det.MergeTarget == "" {
		b.WriteString("\n# merge_target: develop            # branch that prune --merged checks against\n")
	}

	writeHooksComment(&b)
	writeEditorComment(&b)
	return b.String()
}

func vite(project string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeMergeTarget(&b, det)

	writeEnvFileBlock(&b, envTarget(det))
	b.WriteString("\nenv:\n")
	b.WriteString("  PORT: \"{port}\"\n")
	writeResolveComment(&b)

	b.WriteString("\ncommands:\n")
	b.WriteString("  setup:\n")
	fmt.Fprintf(&b, "    - %s\n", installCmd(det))
	b.WriteString("  start: npx vite --port {port} --host\n")

	writeHooksComment(&b)
	writeEditorComment(&b)
	return b.String()
}

func node(project string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeMergeTarget(&b, det)

	if shouldEmitEnv(det) {
		writeEnvFileBlock(&b, envTarget(det))
		b.WriteString("\nenv:\n")
		b.WriteString("  PORT: \"{port}\"\n")
		writeResolveComment(&b)
	}

	b.WriteString("\ncommands:\n")
	b.WriteString("  setup:\n")
	fmt.Fprintf(&b, "    - %s\n", installCmd(det))
	writeStartCommand(&b, det)
	if shouldEmitEnv(det) && !det.HasDotenv {
		b.WriteString("\n# Note: ensure dotenv or similar is installed to auto-load this file\n")
	}
	writeHooksComment(&b)
	writeEditorComment(&b)
	return b.String()
}

func python(project string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeMergeTarget(&b, det)

	if shouldEmitEnv(det) {
		writeEnvFileBlock(&b, envTarget(det))
		b.WriteString("\nenv:\n")
		b.WriteString("  PORT: \"{port}\"\n")
		if det.DBAdapter == "" && !det.HasPrisma {
			b.WriteString(`  # DATABASE_URL: "postgresql://localhost:5432/{database}"` + "\n")
		}
		writeResolveComment(&b)
	}

	b.WriteString("\ncommands:\n")
	b.WriteString("  setup:\n")
	b.WriteString("    - pip install -r requirements.txt\n")
	writeStartCommand(&b, det)
	writeHooksComment(&b)
	writeEditorComment(&b)
	return b.String()
}

func generic(project string, det *detect.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", project)
	writeMergeTarget(&b, det)

	if shouldEmitEnv(det) {
		writeEnvFileBlock(&b, envTarget(det))
		b.WriteString("\nenv:\n")
		b.WriteString("  PORT: \"{port}\"\n")
		writeResolveComment(&b)
	}

	writeHooksComment(&b)
	writeEditorComment(&b)
	return b.String()
}

func writeHooksComment(b *strings.Builder) {
	b.WriteString("\n# hooks:\n")
	b.WriteString("#   pre_setup:\n")
	b.WriteString("#     - echo \"before setup\"                 # aborts setup on failure\n")
	b.WriteString("#   post_setup:\n")
	b.WriteString("#     - echo \"after setup\"                  # warns on failure\n")
}

func writeResolveComment(b *strings.Builder) {
	b.WriteString("  # API_URL: \"{resolve:api}\"               # cross-worktree service URL (same-branch default)\n")
}

func writeEditorComment(b *strings.Builder) {
	b.WriteString("\n# editor:\n")
	b.WriteString("#   title: \"{project} :{port} ({branch})\"  # window title (VS Code, Cursor)\n")
	b.WriteString("#   color: auto                             # title/status bar color (\"auto\" = branch hash, or hex like \"#1a5276\")\n")
	b.WriteString("#   theme: \"\"                               # full IDE theme override (e.g. \"Monokai\", \"GitHub Dark\")\n")
}

func writeEnvFileBlock(b *strings.Builder, envFile string) {
	fmt.Fprintf(b, "\nenv_file: %s\n", envFile)
}

// shouldEmitEnv returns true if the template should include env_file and env blocks.
func shouldEmitEnv(det *detect.Result) bool {
	return det.HasEnvFile || det.AutoLoadsEnvFile()
}

// envTarget returns the env file name to use in the template.
func envTarget(det *detect.Result) string {
	if det.EnvFile != "" {
		return det.EnvFile
	}
	return det.DefaultEnvTarget()
}

func writeStartCommand(b *strings.Builder, det *detect.Result) {
	if cmd := startCommandFor(det); cmd != "" {
		fmt.Fprintf(b, "  start: %s\n", cmd)
	}
}

func startCommandFor(det *detect.Result) string {
	switch det.Framework {
	case "nextjs":
		return runCmd(det) + " dev"
	case "vite":
		return "" // already emitted inline for vite (npx vite)
	case "rails":
		return "" // already emitted inline for rails (bin/dev)
	case "node":
		return runCmd(det) + " dev"
	case "django", "python":
		return "python manage.py runserver 0.0.0.0:{port}"
	default:
		return ""
	}
}

func runCmd(det *detect.Result) string {
	switch det.PackageManager {
	case "yarn":
		return "yarn"
	case "pnpm":
		return "pnpm"
	default:
		return "npm run"
	}
}

func writeMergeTarget(b *strings.Builder, det *detect.Result) {
	if det.MergeTarget != "" && det.MergeTarget != "main" {
		fmt.Fprintf(b, "merge_target: %s\n", det.MergeTarget)
	}
}

// PortHint returns framework-specific guidance on wiring the allocated PORT
// into the dev server. Returns empty string if no hint is needed.
func PortHint(det *detect.Result) string {
	switch det.Framework {
	case "nextjs":
		return `Next.js reads PORT from the environment but not from .env files for the dev server.
Use {port} in your start command:

  commands:
    start: npm run dev -- --port {port}`
	case "vite":
		return `Vite ignores the PORT env var for the dev server.
Use {port} in your start command (gtl init does this automatically):

  commands:
    start: npx vite --port {port} --host`
	case "node":
		return `Ensure your server reads the allocated port from the environment:

  const port = process.env.PORT || 3000;
  app.listen(port);`
	case "django", "python":
		return `Use {port} in your start command for the allocated port:

  commands:
    start: python manage.py runserver 0.0.0.0:{port}`
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
