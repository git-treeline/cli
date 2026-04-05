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
	"runtime"
	"time"

	"github.com/git-treeline/git-treeline/internal/platform"
)

var certsDirFunc = defaultCertsDir

func defaultCertsDir() string {
	return filepath.Join(platform.ConfigDir(), "certs")
}

func certsDir() string {
	return certsDirFunc()
}

// resolveCert returns a TLS certificate for localhost. It tries mkcert first
// (producing a locally-trusted cert), then falls back to a self-signed cert.
// Used by gtl proxy --tls for simple one-off forwarding.
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

// --- Local CA for gtl serve (trusted HTTPS on *.localhost) ---

func caKeyPath() string  { return filepath.Join(certsDir(), "ca-key.pem") }
func caCertPath() string { return filepath.Join(certsDir(), "ca.pem") }

// CACertPath returns the path to the CA certificate (for display to the user).
func CACertPath() string { return caCertPath() }

func serverCertPath() string { return filepath.Join(certsDir(), "server.pem") }
func serverKeyPath() string  { return filepath.Join(certsDir(), "server-key.pem") }

// EnsureCA generates a local CA if one doesn't already exist. Returns
// the path to the CA certificate (for trusting).
func EnsureCA() (string, error) {
	if err := os.MkdirAll(certsDir(), 0o700); err != nil {
		return "", err
	}

	if _, err := os.Stat(caCertPath()); err == nil {
		if _, err := os.Stat(caKeyPath()); err == nil {
			return caCertPath(), nil
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generating CA key: %w", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{"git-treeline"}, CommonName: "git-treeline Local CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", fmt.Errorf("creating CA certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(caCertPath(), certPEM, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(caKeyPath(), keyPEM, 0o600); err != nil {
		return "", err
	}

	return caCertPath(), nil
}

// EnsureServerCert generates a wildcard *.localhost server cert signed by
// the local CA. Regenerates if the cert doesn't exist or is expired.
func EnsureServerCert() (*tls.Certificate, error) {
	if cert, err := tls.LoadX509KeyPair(serverCertPath(), serverKeyPath()); err == nil {
		leaf, parseErr := x509.ParseCertificate(cert.Certificate[0])
		if parseErr == nil && time.Now().Before(leaf.NotAfter.Add(-24*time.Hour)) {
			return &cert, nil
		}
	}

	caKeyPEM, err := os.ReadFile(caKeyPath())
	if err != nil {
		return nil, fmt.Errorf("CA key not found — run 'gtl serve install': %w", err)
	}
	caCertPEM, err := os.ReadFile(caCertPath())
	if err != nil {
		return nil, fmt.Errorf("CA cert not found — run 'gtl serve install': %w", err)
	}

	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return nil, fmt.Errorf("CA key file is not valid PEM")
	}
	caKey, err := x509.ParseECPrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing CA key: %w", err)
	}

	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return nil, fmt.Errorf("CA cert file is not valid PEM")
	}
	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing CA cert: %w", err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"git-treeline"}, CommonName: "*.localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(825 * 24 * time.Hour), // ~2.25 years, under macOS 825-day limit
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", "*.localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("creating server certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(serverCertPath(), certPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(serverKeyPath(), keyPEM, 0o600); err != nil {
		return nil, err
	}

	cert, err := tls.LoadX509KeyPair(serverCertPath(), serverKeyPath())
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// TrustCA adds the local CA certificate to the system trust store.
// Requires sudo on macOS and Linux.
func TrustCA(caCertFile string) error {
	switch runtime.GOOS {
	case "darwin":
		return trustDarwin(caCertFile)
	case "linux":
		return trustLinux(caCertFile)
	default:
		return fmt.Errorf("CA trust not supported on %s", runtime.GOOS)
	}
}

// UntrustCA removes the local CA from the system trust store.
func UntrustCA() error {
	switch runtime.GOOS {
	case "darwin":
		return untrustDarwin()
	case "linux":
		return untrustLinux()
	default:
		return nil
	}
}

// IsCAInstalled checks whether the local CA has been generated.
func IsCAInstalled() bool {
	_, err := os.Stat(caCertPath())
	return err == nil
}

func trustDarwin(caCertFile string) error {
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password (1 of 2): ",
		"security", "add-trusted-cert",
		"-d", "-r", "trustRoot",
		"-k", "/Library/Keychains/System.keychain",
		caCertFile)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func untrustDarwin() error {
	if !IsCAInstalled() {
		return nil
	}
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to remove git-treeline CA: ",
		"security", "remove-trusted-cert",
		"-d", caCertPath())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return nil
}

const linuxCACopyPath = "/usr/local/share/ca-certificates/git-treeline.crt"

func trustLinux(caCertFile string) error {
	script := fmt.Sprintf("cp '%s' '%s' && update-ca-certificates", caCertFile, linuxCACopyPath)
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password (1 of 2): ",
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func untrustLinux() error {
	script := fmt.Sprintf("rm -f '%s' && update-ca-certificates --fresh", linuxCACopyPath)
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to remove git-treeline CA: ",
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return nil
}
