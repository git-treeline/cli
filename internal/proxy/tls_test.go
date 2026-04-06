package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestNewCertManager_WithoutCA_Fails(t *testing.T) {
	withTempCertsDir(t)

	_, err := NewCertManager()
	if err == nil {
		t.Error("expected error when CA doesn't exist")
	}
}

func TestCertManager_GetCertificate(t *testing.T) {
	withTempCertsDir(t)
	if _, err := EnsureCA(); err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	cm, err := NewCertManager()
	if err != nil {
		t.Fatalf("NewCertManager: %v", err)
	}

	hello := &tls.ClientHelloInfo{ServerName: "website-main.localhost"}
	cert, err := cm.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parsing leaf: %v", err)
	}

	found := false
	for _, name := range leaf.DNSNames {
		if name == "website-main.localhost" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected website-main.localhost in SAN, got %v", leaf.DNSNames)
	}

	if len(cert.Certificate) != 2 {
		t.Errorf("expected cert chain of length 2 (leaf + CA), got %d", len(cert.Certificate))
	}
}

func TestCertManager_CachesPerHostname(t *testing.T) {
	withTempCertsDir(t)
	if _, err := EnsureCA(); err != nil {
		t.Fatal(err)
	}

	cm, err := NewCertManager()
	if err != nil {
		t.Fatal(err)
	}

	hello := &tls.ClientHelloInfo{ServerName: "app-main.localhost"}
	cert1, _ := cm.GetCertificate(hello)
	cert2, _ := cm.GetCertificate(hello)

	if &cert1.Certificate[0][0] != &cert2.Certificate[0][0] {
		t.Error("expected same cert pointer on second call (cache hit)")
	}
}

func TestCertManager_DifferentHostnames(t *testing.T) {
	withTempCertsDir(t)
	if _, err := EnsureCA(); err != nil {
		t.Fatal(err)
	}

	cm, err := NewCertManager()
	if err != nil {
		t.Fatal(err)
	}

	c1, _ := cm.GetCertificate(&tls.ClientHelloInfo{ServerName: "a.localhost"})
	c2, _ := cm.GetCertificate(&tls.ClientHelloInfo{ServerName: "b.localhost"})

	leaf1, _ := x509.ParseCertificate(c1.Certificate[0])
	leaf2, _ := x509.ParseCertificate(c2.Certificate[0])

	if leaf1.SerialNumber.Cmp(leaf2.SerialNumber) == 0 {
		t.Error("expected different serial numbers for different hostnames")
	}
}

func TestCertManager_EmptyServerName(t *testing.T) {
	withTempCertsDir(t)
	if _, err := EnsureCA(); err != nil {
		t.Fatal(err)
	}

	cm, err := NewCertManager()
	if err != nil {
		t.Fatal(err)
	}

	cert, err := cm.GetCertificate(&tls.ClientHelloInfo{ServerName: ""})
	if err != nil {
		t.Fatalf("GetCertificate with empty ServerName: %v", err)
	}

	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	if leaf.Subject.CommonName != "localhost" {
		t.Errorf("expected CN=localhost for empty ServerName, got %s", leaf.Subject.CommonName)
	}
}

func TestCertManager_ConcurrentAccess(t *testing.T) {
	withTempCertsDir(t)
	if _, err := EnsureCA(); err != nil {
		t.Fatal(err)
	}

	cm, err := NewCertManager()
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cm.GetCertificate(&tls.ClientHelloInfo{ServerName: "race.localhost"})
			if err != nil {
				t.Errorf("concurrent GetCertificate failed: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestCertNeedsRegeneration_MissingFile(t *testing.T) {
	if !certNeedsRegeneration("/nonexistent/cert.pem") {
		t.Error("missing file should need regeneration")
	}
}

func TestCertNeedsRegeneration_CorruptFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "corrupt.pem")
	_ = os.WriteFile(f, []byte("not a cert"), 0o644)
	if !certNeedsRegeneration(f) {
		t.Error("corrupt file should need regeneration")
	}
}

func TestCertNeedsRegeneration_ExpiringSoon(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "expiring.pem")
	writeTestCert(t, certFile, time.Now().Add(3*24*time.Hour))
	if !certNeedsRegeneration(certFile) {
		t.Error("cert expiring in 3 days should need regeneration (buffer is 7 days)")
	}
}

func TestCertNeedsRegeneration_Valid(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "valid.pem")
	writeTestCert(t, certFile, time.Now().Add(365*24*time.Hour))
	if certNeedsRegeneration(certFile) {
		t.Error("cert valid for a year should not need regeneration")
	}
}

func writeTestCert(t *testing.T, path string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(path)
	_ = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	_ = f.Close()
}

func TestCertManager_VerifiesAgainstCA(t *testing.T) {
	withTempCertsDir(t)
	if _, err := EnsureCA(); err != nil {
		t.Fatal(err)
	}

	cm, err := NewCertManager()
	if err != nil {
		t.Fatal(err)
	}

	cert, _ := cm.GetCertificate(&tls.ClientHelloInfo{ServerName: "test-feat.localhost"})

	roots := x509.NewCertPool()
	roots.AddCert(cm.caCert)

	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:   roots,
		DNSName: "test-feat.localhost",
	})
	if err != nil {
		t.Errorf("cert did not verify against CA for hostname: %v", err)
	}
}
