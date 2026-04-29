package templates

import (
	"fmt"

	"github.com/git-treeline/git-treeline/internal/detect"
)

// Diagnostic represents an advisory message from post-init analysis.
type Diagnostic struct {
	Level   string // "info", "warn"
	Message string
}

// Diagnose runs framework-aware checks and returns actionable diagnostics.
func Diagnose(det *detect.Result) []Diagnostic {
	var diags []Diagnostic

	diags = append(diags, diagnoseEnvLoading(det)...)
	diags = append(diags, diagnosePortWiring(det)...)
	diags = append(diags, diagnoseUmbrella(det)...)

	return diags
}

func diagnoseUmbrella(det *detect.Result) []Diagnostic {
	if !det.IsUmbrella {
		return nil
	}
	return []Diagnostic{{
		Level: "warn",
		Message: "Phoenix umbrella project detected.\n" +
			"  gtl treats the umbrella as a single app — per-app port/db isolation isn't supported yet.\n" +
			"  File an issue at https://github.com/git-treeline/git-treeline if you need it.",
	}}
}

func diagnoseEnvLoading(det *detect.Result) []Diagnostic {
	var diags []Diagnostic

	target := envTarget(det)

	if det.HasEnvFile {
		diags = append(diags, Diagnostic{
			Level:   "info",
			Message: fmt.Sprintf("Found %s — Treeline will write allocated values here.", det.EnvFile),
		})
	} else if det.AutoLoadsEnvFile() {
		diags = append(diags, Diagnostic{
			Level:   "info",
			Message: fmt.Sprintf("%s auto-loads %s — Treeline will create it in each worktree.", frameworkName(det), target),
		})
	} else if det.HasDotenv {
		diags = append(diags, Diagnostic{
			Level:   "info",
			Message: fmt.Sprintf("dotenv detected — Treeline will create %s in each worktree.", target),
		})
	} else {
		switch det.Framework {
		case "node":
			diags = append(diags, Diagnostic{
				Level:   "warn",
				Message: "no dotenv library detected.\n  Treeline writes .env but your app won't load it.\n  npm install dotenv",
			})
		case "django", "python":
			diags = append(diags, Diagnostic{
				Level:   "warn",
				Message: "no python-dotenv detected.\n  Treeline writes .env but your app won't load it.\n  pip install python-dotenv",
			})
		case "phoenix":
			diags = append(diags, Diagnostic{
				Level: "info",
				Message: fmt.Sprintf("Phoenix doesn't auto-load %s. The generated start command sets PORT inline.\n  For other env vars, source the file or use a library like dotenvy.",
					target),
			})
		case "go", "rust":
			diags = append(diags, Diagnostic{
				Level: "info",
				Message: fmt.Sprintf("Treeline will write %s but %s apps don't typically auto-load env files.\n  Source it: set -a && . %s && set +a && your-command",
					target, det.Framework, target),
			})
		}
	}

	return diags
}

func diagnosePortWiring(det *detect.Result) []Diagnostic {
	hint := PortHint(det)
	if hint == "" {
		return nil
	}

	return []Diagnostic{{
		Level:   "warn",
		Message: hint,
	}}
}

func frameworkName(det *detect.Result) string {
	switch det.Framework {
	case "nextjs":
		return "Next.js"
	case "vite":
		return "Vite"
	case "rails":
		return "Rails"
	case "phoenix":
		return "Phoenix"
	case "django":
		return "Django"
	case "node":
		return "Node.js"
	default:
		return det.Framework
	}
}
