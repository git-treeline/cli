package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSelfSigned(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	cert, err := generateSelfSigned(certFile, keyFile)
	if err != nil {
		t.Fatalf("generateSelfSigned failed: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatal("expected at least one certificate in chain")
	}

	if _, err := os.Stat(certFile); err != nil {
		t.Errorf("cert file not written: %v", err)
	}
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("key file not written: %v", err)
	}
}

func TestCachedCertIsReused(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	_, err := generateSelfSigned(certFile, keyFile)
	if err != nil {
		t.Fatalf("first generation failed: %v", err)
	}

	info, _ := os.Stat(certFile)
	firstMod := info.ModTime()

	loaded, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("loading cached cert failed: %v", err)
	}

	info2, _ := os.Stat(certFile)
	if info2.ModTime() != firstMod {
		t.Error("cert file was unexpectedly modified")
	}

	if len(loaded.Certificate) == 0 {
		t.Fatal("loaded cert has no certificates")
	}
}

func withTempCertsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := certsDirFunc
	certsDirFunc = func() string { return dir }
	t.Cleanup(func() { certsDirFunc = orig })
	return dir
}

func TestEnsureCA_GeneratesFiles(t *testing.T) {
	dir := withTempCertsDir(t)

	caPath, err := EnsureCA()
	if err != nil {
		t.Fatalf("EnsureCA failed: %v", err)
	}

	if caPath != filepath.Join(dir, "ca.pem") {
		t.Errorf("unexpected CA path: %s", caPath)
	}

	if _, err := os.Stat(filepath.Join(dir, "ca.pem")); err != nil {
		t.Error("CA cert file not created")
	}
	if _, err := os.Stat(filepath.Join(dir, "ca-key.pem")); err != nil {
		t.Error("CA key file not created")
	}
}

func TestEnsureCA_Idempotent(t *testing.T) {
	withTempCertsDir(t)

	_, err := EnsureCA()
	if err != nil {
		t.Fatalf("first EnsureCA failed: %v", err)
	}

	info, _ := os.Stat(caCertPath())
	firstMod := info.ModTime()

	_, err = EnsureCA()
	if err != nil {
		t.Fatalf("second EnsureCA failed: %v", err)
	}

	info2, _ := os.Stat(caCertPath())
	if info2.ModTime() != firstMod {
		t.Error("CA cert was regenerated on second call")
	}
}

func TestEnsureServerCert_GeneratesValidCert(t *testing.T) {
	withTempCertsDir(t)

	if _, err := EnsureCA(); err != nil {
		t.Fatalf("EnsureCA failed: %v", err)
	}

	cert, err := EnsureServerCert()
	if err != nil {
		t.Fatalf("EnsureServerCert failed: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatal("expected at least one certificate")
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parsing leaf cert: %v", err)
	}

	foundLocalhost := false
	for _, name := range leaf.DNSNames {
		if name == "*.localhost" {
			foundLocalhost = true
		}
	}
	if !foundLocalhost {
		t.Errorf("expected *.localhost in SAN, got %v", leaf.DNSNames)
	}
}

func TestEnsureServerCert_ReusesCached(t *testing.T) {
	withTempCertsDir(t)

	if _, err := EnsureCA(); err != nil {
		t.Fatal(err)
	}

	_, err := EnsureServerCert()
	if err != nil {
		t.Fatal(err)
	}

	info, _ := os.Stat(serverCertPath())
	firstMod := info.ModTime()

	_, err = EnsureServerCert()
	if err != nil {
		t.Fatal(err)
	}

	info2, _ := os.Stat(serverCertPath())
	if info2.ModTime() != firstMod {
		t.Error("server cert was regenerated on second call")
	}
}

func TestEnsureServerCert_WithoutCA_Fails(t *testing.T) {
	withTempCertsDir(t)

	_, err := EnsureServerCert()
	if err == nil {
		t.Error("expected error when CA doesn't exist")
	}
}
