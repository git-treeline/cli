package tunnel

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func setupCloudflaredDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".cloudflared")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHasTunnelCredentials_FalseWhenMissing(t *testing.T) {
	setupCloudflaredDir(t)
	if HasTunnelCredentials("any") {
		t.Error("expected false with empty dir")
	}
}

func TestHasTunnelCredentials_TrueForNameBasedFallback(t *testing.T) {
	dir := setupCloudflaredDir(t)
	touch(t, filepath.Join(dir, "my-tunnel.json"))
	if !HasTunnelCredentials("my-tunnel") {
		t.Error("expected true when name.json exists")
	}
}

func TestHasTunnelCredentials_IgnoresDirectory(t *testing.T) {
	dir := setupCloudflaredDir(t)
	if err := os.Mkdir(filepath.Join(dir, "weird.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if HasTunnelCredentials("weird") {
		t.Error("expected false for directory at credentials path")
	}
}

func TestFindDomainCerts(t *testing.T) {
	dir := setupCloudflaredDir(t)
	touch(t, filepath.Join(dir, "cert.pem"))               // default — should NOT be returned
	touch(t, filepath.Join(dir, "cert-example.com.pem"))   // per-domain — included
	touch(t, filepath.Join(dir, "cert-other.dev.pem"))     // per-domain — included
	touch(t, filepath.Join(dir, "some-uuid.json"))         // credentials — not a cert
	touch(t, filepath.Join(dir, "cert-no-pem-suffix.txt")) // wrong suffix — excluded

	got := FindDomainCerts()
	sort.Strings(got)
	want := []string{
		filepath.Join(dir, "cert-example.com.pem"),
		filepath.Join(dir, "cert-other.dev.pem"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d certs, want %d: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFindTunnelCredentialFiles_NameBased(t *testing.T) {
	dir := setupCloudflaredDir(t)
	touch(t, filepath.Join(dir, "alpha.json"))
	touch(t, filepath.Join(dir, "beta.json"))
	touch(t, filepath.Join(dir, "gamma.json"))

	got := FindTunnelCredentialFiles([]string{"alpha", "beta"})
	if len(got) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "alpha.json") || !strings.Contains(got[1], "beta.json") {
		t.Errorf("unexpected paths: %v", got)
	}
}

func TestFindTunnelCredentialFiles_SkipsMissing(t *testing.T) {
	setupCloudflaredDir(t)
	got := FindTunnelCredentialFiles([]string{"never-existed"})
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestDeleteCloudflaredFiles_RemovesExisting(t *testing.T) {
	dir := setupCloudflaredDir(t)
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	touch(t, a)
	touch(t, b)

	removed := DeleteCloudflaredFiles([]string{a, b, filepath.Join(dir, "missing.json")})
	if len(removed) != 2 {
		t.Errorf("expected 2 removed, got %d: %v", len(removed), removed)
	}
	if fileExists(a) || fileExists(b) {
		t.Error("files should have been deleted")
	}
}

func TestDefaultCertPath(t *testing.T) {
	setupCloudflaredDir(t)
	got := DefaultCertPath()
	if !strings.HasSuffix(got, "/.cloudflared/cert.pem") {
		t.Errorf("unexpected default cert path: %s", got)
	}
}
