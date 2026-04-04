package proxy

import (
	"crypto/tls"
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
