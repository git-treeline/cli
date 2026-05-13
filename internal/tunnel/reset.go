package tunnel

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CredentialsPath returns the absolute path where the connector
// credentials JSON for a named tunnel should live. Prefers the canonical
// cloudflared format (<UUID>.json) when the UUID can be resolved;
// otherwise falls back to <tunnelName>.json — matching what
// findCredentialsFile writes into the generated config.
func CredentialsPath(tunnelName string) string {
	return findCredentialsFile(tunnelName)
}

// HasTunnelCredentials reports whether a connector credentials JSON for
// the named tunnel exists locally. Cloudflare doesn't distribute these
// through the API — they're only on the machine that ran
// `cloudflared tunnel create`. Granting account access lets you list and
// manage a tunnel but does NOT give you its credentials; without the
// JSON file cloudflared exits at startup with "credentials file ...
// doesn't exist or is not a file".
func HasTunnelCredentials(tunnelName string) bool {
	dir := ConfigDir()
	if id := lookupTunnelID(tunnelName); id != "" {
		if info, err := os.Stat(filepath.Join(dir, id+".json")); err == nil && !info.IsDir() {
			return true
		}
	}
	if info, err := os.Stat(filepath.Join(dir, tunnelName+".json")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

// FindDomainCerts returns absolute paths of per-domain cert files
// (cert-<domain>.pem) in the cloudflared config dir. Used by reset to
// enumerate what would be deleted.
func FindDomainCerts() []string {
	dir := ConfigDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "cert-") || !strings.HasSuffix(name, ".pem") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out
}

// FindTunnelCredentialFiles returns absolute paths of credential JSONs
// associated with the given tunnel names. Includes both the UUID-based
// path (canonical cloudflared format) and the name-based fallback. Only
// returns paths that actually exist on disk.
func FindTunnelCredentialFiles(tunnelNames []string) []string {
	dir := ConfigDir()
	seen := map[string]bool{}
	for _, name := range tunnelNames {
		if id := lookupTunnelID(name); id != "" {
			p := filepath.Join(dir, id+".json")
			if fileExists(p) {
				seen[p] = true
			}
		}
		p := filepath.Join(dir, name+".json")
		if fileExists(p) {
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// DefaultCertPath returns the path to the default Cloudflare cert.pem.
func DefaultCertPath() string {
	return filepath.Join(ConfigDir(), "cert.pem")
}

// DeleteCloudflaredFiles removes the given files, ignoring errors for
// paths that don't exist. Returns the list of paths actually removed.
func DeleteCloudflaredFiles(paths []string) []string {
	var removed []string
	for _, p := range paths {
		if err := os.Remove(p); err == nil {
			removed = append(removed, p)
		}
	}
	return removed
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
