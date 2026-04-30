package templates

import (
	"fmt"
	"strings"

	"github.com/git-treeline/cli/internal/detect"
)

// TunnelHint returns framework-specific guidance for whitelisting a tunnel
// domain so the dev server accepts requests from it. Returns empty string
// if no configuration is needed.
//
// For named tunnels, pass the full hostname (e.g. "myapp-feature.example.dev")
// and domain (e.g. "example.dev"). For quick tunnels, pass hostname="" and
// domain="" to get a generic hint.
func TunnelHint(det *detect.Result, hostname, domain string) string {
	if hostname == "" {
		return tunnelHintQuick(det)
	}
	return tunnelHintNamed(det, hostname, domain)
}

func tunnelHintNamed(det *detect.Result, hostname, domain string) string {
	wildcard := "." + domain

	switch det.Framework {
	case "rails":
		return fmt.Sprintf(`Rails blocks unknown hosts in development. Add to development.rb:

    config.hosts << "%s"`, wildcard)

	case "vite":
		return fmt.Sprintf(`Vite rejects non-localhost hostnames (403). Add to vite.config:

    server: { allowedHosts: ["%s"] }`, wildcard)

	case "django", "python":
		return fmt.Sprintf(`Django rejects requests when the Host header isn't in ALLOWED_HOSTS.
  Add to settings.py:

    ALLOWED_HOSTS += ["%s"]`, hostname)

	default:
		return ""
	}
}

func tunnelHintQuick(det *detect.Result) string {
	switch det.Framework {
	case "rails":
		return `Rails blocks unknown hosts in development. Add to development.rb:

    config.hosts << ".trycloudflare.com"

  For a stable domain: gtl tunnel setup`

	case "vite":
		return `Vite rejects non-localhost hostnames (403). Add to vite.config:

    server: { allowedHosts: [".trycloudflare.com"] }

  For a stable domain: gtl tunnel setup`

	case "django", "python":
		return `Django rejects unknown hosts. Add to settings.py:

    ALLOWED_HOSTS += [".trycloudflare.com"]

  For a stable domain: gtl tunnel setup`

	default:
		return ""
	}
}

// FormatTunnelHint wraps a TunnelHint in a visible block for terminal output.
// Returns empty string if there's no hint.
func FormatTunnelHint(hint string) string {
	if hint == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, line := range strings.Split(hint, "\n") {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}
