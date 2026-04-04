package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/git-treeline/git-treeline/internal/platform"
)

func certsDir() string {
	return filepath.Join(platform.ConfigDir(), "certs")
}

// resolveCert returns a TLS certificate for localhost. It tries mkcert first
// (producing a locally-trusted cert), then falls back to a self-signed cert.
// Certificates are cached on disk so mkcert isn't re-invoked on every run.
func resolveCert() (*tls.Certificate, error) {
	dir := certsDir()
	certFile := filepath.Join(dir, "localhost.pem")
	keyFile := filepath.Join(dir, "localhost-key.pem")

	if cert, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		return &cert, nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	if mkcertPath, err := exec.LookPath("mkcert"); err == nil {
		return generateMkcert(mkcertPath, certFile, keyFile)
	}

	fmt.Fprintln(os.Stderr, "Warning: mkcert not found. Using self-signed certificate.")
	fmt.Fprintln(os.Stderr, "  Install mkcert for trusted local HTTPS: https://github.com/FiloSottile/mkcert")
	return generateSelfSigned(certFile, keyFile)
}

func generateMkcert(mkcertPath, certFile, keyFile string) (*tls.Certificate, error) {
	cmd := exec.Command(mkcertPath,
		"-cert-file", certFile,
		"-key-file", keyFile,
		"localhost", "127.0.0.1", "::1",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("mkcert failed: %w", err)
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

func generateSelfSigned(certFile, keyFile string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"git-treeline dev proxy"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return nil, err
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}
