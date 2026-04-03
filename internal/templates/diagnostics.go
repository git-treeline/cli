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

	return diags
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
				Message: "No dotenv library detected. Treeline writes .env but your app won't read it without dotenv.\n  npm install dotenv\n  Then add: require('dotenv').config() to your entry point.",
			})
		case "django", "python":
			diags = append(diags, Diagnostic{
				Level:   "warn",
				Message: "No python-dotenv or django-environ detected. Treeline writes .env but your app won't read it.\n  pip install python-dotenv\n  Then load it in manage.py or settings.py.",
			})
		case "go", "rust":
			diags = append(diags, Diagnostic{
				Level: "info",
				Message: fmt.Sprintf("Treeline will write %s but %s apps don't typically auto-load env files.\n  Source it in your start command: set -a && . %s && set +a && your-command",
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
	case "django":
		return "Django"
	case "node":
		return "Node.js"
	default:
		return det.Framework
	}
}
