package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratePlist(t *testing.T) {
	content, err := GeneratePlist("/usr/local/bin/gtl")
	if err != nil {
		t.Fatalf("GeneratePlist failed: %v", err)
	}

	checks := []string{
		"<string>dev.treeline.router</string>",
		"<string>/usr/local/bin/gtl</string>",
		"<string>serve</string>",
		"<string>run</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"router.log",
		"router.err",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("plist missing %q", check)
		}
	}
}

func TestResolveStablePath_NonCellar(t *testing.T) {
	got := resolveStablePath("/usr/local/bin/gtl")
	if got != "/usr/local/bin/gtl" {
		t.Errorf("expected unchanged path, got %q", got)
	}
}

func TestResolveStablePath_CellarWithSymlink(t *testing.T) {
	// Simulate: <tmp>/Cellar/git-treeline/0.38.0/bin/gtl → real binary
	//           <tmp>/bin/gtl → symlink to ../Cellar/git-treeline/0.38.0/bin/gtl
	root := t.TempDir()

	cellarBin := filepath.Join(root, "Cellar", "git-treeline", "0.38.0", "bin")
	_ = os.MkdirAll(cellarBin, 0o755)
	realBinary := filepath.Join(cellarBin, "gtl")
	_ = os.WriteFile(realBinary, []byte("binary"), 0o755)

	stableBin := filepath.Join(root, "bin")
	_ = os.MkdirAll(stableBin, 0o755)
	stableLink := filepath.Join(stableBin, "gtl")
	_ = os.Symlink(filepath.Join("..", "Cellar", "git-treeline", "0.38.0", "bin", "gtl"), stableLink)

	got := resolveStablePath(realBinary)
	if got != stableLink {
		t.Errorf("expected stable symlink %q, got %q", stableLink, got)
	}
}

func TestResolveStablePath_CellarNoSymlink(t *testing.T) {
	root := t.TempDir()

	cellarBin := filepath.Join(root, "Cellar", "git-treeline", "0.38.0", "bin")
	_ = os.MkdirAll(cellarBin, 0o755)
	realBinary := filepath.Join(cellarBin, "gtl")
	_ = os.WriteFile(realBinary, []byte("binary"), 0o755)

	// No symlink in <root>/bin — should return original
	got := resolveStablePath(realBinary)
	if got != realBinary {
		t.Errorf("expected original path %q, got %q", realBinary, got)
	}
}

func TestResolveStablePath_GoInstall(t *testing.T) {
	// go install puts binary in ~/go/bin/gtl — no Cellar, should be unchanged
	got := resolveStablePath("/Users/someone/go/bin/gtl")
	if got != "/Users/someone/go/bin/gtl" {
		t.Errorf("expected unchanged path, got %q", got)
	}
}

func TestGenerateUnit(t *testing.T) {
	content, err := GenerateUnit("/usr/local/bin/gtl")
	if err != nil {
		t.Fatalf("GenerateUnit failed: %v", err)
	}

	checks := []string{
		"ExecStart=/usr/local/bin/gtl serve run",
		"Restart=always",
		"WantedBy=default.target",
		"git-treeline subdomain router",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("unit missing %q", check)
		}
	}
}
