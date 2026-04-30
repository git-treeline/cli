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
	"strings"
	"sync"
	"time"

	"github.com/git-treeline/cli/internal/platform"
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

const certRenewalBuffer = 7 * 24 * time.Hour

func certNeedsRegeneration(certFile string) bool {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return true
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return true
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	return time.Until(cert.NotAfter) < certRenewalBuffer
}

// EnsureCA generates a local CA if one doesn't exist or is expiring within 7
// days. Returns the path to the CA certificate (for trusting).
func EnsureCA() (string, error) {
	if err := os.MkdirAll(certsDir(), 0o700); err != nil {
		return "", err
	}

	if !certNeedsRegeneration(caCertPath()) {
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

// CertManager generates per-hostname TLS certificates on demand, signed by
// the local CA. This avoids wildcard certs (*.localhost) which Safari and
// Chromium reject for the .localhost TLD.
type CertManager struct {
	caKey  *ecdsa.PrivateKey
	caCert *x509.Certificate
	mu     sync.RWMutex
	cache  map[string]*tls.Certificate
}

// NewCertManager loads the local CA and returns a manager that generates
// per-hostname certs via GetCertificate. Returns an error if the CA hasn't
// been created yet (user needs to run 'gtl serve install').
func NewCertManager() (*CertManager, error) {
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

	return &CertManager{
		caKey:  caKey,
		caCert: caCert,
		cache:  make(map[string]*tls.Certificate),
	}, nil
}

const maxCachedCerts = 1000

// GetCertificate is a tls.Config callback that returns a cert with an exact
// SAN for the requested hostname. The cache is capped to prevent unbounded
// growth from garbage hostnames.
func (cm *CertManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	hostname := hello.ServerName
	if hostname == "" {
		hostname = "localhost"
	}

	cm.mu.RLock()
	cert, ok := cm.cache[hostname]
	cm.mu.RUnlock()
	if ok {
		leaf, _ := x509.ParseCertificate(cert.Certificate[0])
		if leaf != nil && time.Until(leaf.NotAfter) > certRenewalBuffer {
			return cert, nil
		}
	}

	cert, err := cm.issueCert(hostname)
	if err != nil {
		return nil, err
	}

	cm.mu.Lock()
	if len(cm.cache) < maxCachedCerts {
		cm.cache[hostname] = cert
	}
	cm.mu.Unlock()
	return cert, nil
}

func (cm *CertManager) issueCert(hostname string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"git-treeline"}, CommonName: hostname},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(825 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{hostname, "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, cm.caCert, &key.PublicKey, cm.caKey)
	if err != nil {
		return nil, fmt.Errorf("creating certificate for %s: %w", hostname, err)
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{certDER, cm.caCert.Raw},
		PrivateKey:  key,
	}
	return &tlsCert, nil
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

// CACertExpiry returns the CA certificate's NotAfter time, or an error
// if the cert can't be read/parsed. Used by gtl doctor for cert health.
func CACertExpiry() (time.Time, error) {
	data, err := os.ReadFile(caCertPath())
	if err != nil {
		return time.Time{}, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return time.Time{}, fmt.Errorf("CA cert is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}

func trustDarwin(caCertFile string) error {
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password (1 of 2): ",
		"/usr/bin/security", "add-trusted-cert",
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
		"/usr/bin/security", "remove-trusted-cert",
		"-d", caCertPath())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to remove CA from system keychain: %w", err)
	}
	return nil
}

type linuxTrustConfig struct {
	certDir       string
	updateCommand string
}

var linuxTrustConfigs = map[string]linuxTrustConfig{
	"debian": {"/usr/local/share/ca-certificates", "update-ca-certificates"},
	"arch":   {"/etc/ca-certificates/trust-source/anchors", "update-ca-trust"},
	"fedora": {"/etc/pki/ca-trust/source/anchors", "update-ca-trust"},
	"suse":   {"/etc/pki/trust/anchors", "update-ca-certificates"},
}

func detectLinuxDistro() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "debian"
	}
	content := strings.ToLower(string(data))
	switch {
	case strings.Contains(content, "arch"):
		return "arch"
	case strings.Contains(content, "fedora"),
		strings.Contains(content, "rhel"),
		strings.Contains(content, "centos"):
		return "fedora"
	case strings.Contains(content, "suse"):
		return "suse"
	default:
		return "debian"
	}
}

func trustLinux(caCertFile string) error {
	cfg := linuxTrustConfigs[detectLinuxDistro()]
	script := fmt.Sprintf("/bin/mkdir -p '%s' && /bin/cp '%s' '%s' && %s",
		cfg.certDir, caCertFile, filepath.Join(cfg.certDir, "git-treeline.crt"), cfg.updateCommand)
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password (1 of 2): ",
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func untrustLinux() error {
	cfg := linuxTrustConfigs[detectLinuxDistro()]
	certFile := filepath.Join(cfg.certDir, "git-treeline.crt")
	script := fmt.Sprintf("/bin/rm -f '%s' && %s", certFile, cfg.updateCommand)
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to remove git-treeline CA: ",
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to remove CA from trust store: %w", err)
	}
	return nil
}
