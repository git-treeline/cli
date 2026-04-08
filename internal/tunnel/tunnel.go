// Package tunnel wraps cloudflared to expose local ports via public HTTPS tunnels.
// Supports quick tunnels (random URL, no account) and named tunnels with BYO domains.
package tunnel

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// RunQuick starts a Cloudflare quick tunnel exposing the given local port.
// The tunnel gets a random *.trycloudflare.com URL. Blocks until interrupted.
func RunQuick(port int) error {
	cfPath, err := ResolveCloudflared()
	if err != nil {
		return err
	}

	cmd := exec.Command(cfPath, "tunnel", "--url", fmt.Sprintf("http://localhost:%d", port))
	cmd.Stdout = os.Stdout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start cloudflared: %w", err)
	}

	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		urlPrinted := false
		for scanner.Scan() {
			line := scanner.Text()
			if !urlPrinted {
				if u := ExtractTrycloudflareURL(line); u != "" {
					fmt.Printf("Tunnel: %s → http://localhost:%d\n", u, port)
					fmt.Println("Press Ctrl+C to stop")
					fmt.Println()
					urlPrinted = true
					continue
				}
			}
			FilterLine(line)
		}
	}()

	return WaitForSignalOrExit(cmd)
}

var trycloudflareRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// ExtractTrycloudflareURL returns the first *.trycloudflare.com URL found in
// the line, or "" if none is present.
func ExtractTrycloudflareURL(line string) string {
	return trycloudflareRe.FindString(line)
}

// RunNamed starts a named Cloudflare tunnel using a generated config file.
// The tunnel must already exist (via Setup) and DNS must be routed.
func RunNamed(tunnelName, domain, routeKey string, port int) error {
	cfPath, err := ResolveCloudflared()
	if err != nil {
		return err
	}

	hostname := routeKey + "." + domain
	configPath, err := writeTunnelConfig(tunnelName, hostname, port)
	if err != nil {
		return fmt.Errorf("failed to write tunnel config: %w", err)
	}

	fmt.Printf("Tunnel: https://%s → http://localhost:%d\n", hostname, port)
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	cmd := exec.Command(cfPath, "tunnel", "--config", configPath, "run", tunnelName)
	cmd.Stdout = os.Stdout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start cloudflared: %w", err)
	}

	go FilterCloudflaredLogs(stderrPipe)

	return WaitForSignalOrExit(cmd)
}

// --- Shared cloudflared process management ---

// FilterCloudflaredLogs reads cloudflared stderr and only passes through
// errors and warnings, suppressing the verbose startup noise.
func FilterCloudflaredLogs(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		FilterLine(scanner.Text())
	}
}

var requestMethodRe = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)

// FilterLine writes a single cloudflared log line to stdout/stderr if it
// looks like an error, warning, or HTTP request.
func FilterLine(line string) {
	filterLineTo(os.Stdout, os.Stderr, line)
}

func filterLineTo(stdout, stderr io.Writer, line string) {
	switch {
	case strings.Contains(line, "ERR"),
		strings.Contains(line, "WRN"),
		strings.Contains(line, "failed"),
		strings.Contains(line, "error"):
		_, _ = fmt.Fprintln(stderr, line)
	case requestMethodRe.MatchString(line):
		_, _ = fmt.Fprintln(stdout, line)
	case strings.Contains(line, "INF") && strings.Contains(line, "Registered"):
		_, _ = fmt.Fprintln(stdout, line)
	}
}

// WaitForSignalOrExit blocks until SIGINT/SIGTERM or the command exits.
// On signal, it sends SIGTERM to the process group.
func WaitForSignalOrExit(cmd *exec.Cmd) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-quit:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		return <-done
	case err := <-done:
		return err
	}
}

// --- Setup / validation helpers ---

// ResolveCloudflared returns the path to cloudflared or an error with install instructions.
func ResolveCloudflared() (string, error) {
	path, err := exec.LookPath("cloudflared")
	if err != nil {
		return "", fmt.Errorf("cloudflared not found in PATH\n  Install: brew install cloudflared\n  Or: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/")
	}
	return path, nil
}

// OfferInstall prompts the user to install cloudflared and attempts the install.
// Returns true if the install was attempted, false if the user declined.
func OfferInstall() bool {
	fmt.Println("    cloudflared is not installed.")
	fmt.Println()

	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			fmt.Println("    Install via Homebrew?")
			fmt.Print("    [y/N] ")
			var answer string
			_, _ = fmt.Scanln(&answer)
			if answer != "y" && answer != "yes" {
				fmt.Println("    Install manually: brew install cloudflared")
				return false
			}
			cmd := exec.Command("brew", "install", "cloudflared")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "    brew install failed: %v\n", err)
				return false
			}
			return true
		}
	}

	fmt.Println("    Install cloudflared to continue:")
	fmt.Println("      macOS:  brew install cloudflared")
	fmt.Println("      Linux:  https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/")
	return false
}

// IsLoggedIn checks whether cloudflared has credentials (cert.pem exists).
func IsLoggedIn() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".cloudflared", "cert.pem"))
	return err == nil
}

// IsLoggedInForDomain checks whether we have a domain-specific cert for the given domain.
func IsLoggedInForDomain(domain string) bool {
	certPath := CertPathForDomain(domain)
	_, err := os.Stat(certPath)
	return err == nil
}

// CertPathForDomain returns the path to a domain-specific cert file.
// For domain "example.com", returns ~/.cloudflared/cert-example.com.pem
func CertPathForDomain(domain string) string {
	return filepath.Join(ConfigDir(), fmt.Sprintf("cert-%s.pem", domain))
}

// Login runs `cloudflared tunnel login` which opens a browser for OAuth.
func Login() error {
	cfPath, err := ResolveCloudflared()
	if err != nil {
		return err
	}

	cmd := exec.Command(cfPath, "tunnel", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// LoginForDomain runs `cloudflared tunnel login` and saves the cert for a specific domain.
// After login, the new cert.pem is moved to cert-{domain}.pem.
func LoginForDomain(domain string) error {
	cfPath, err := ResolveCloudflared()
	if err != nil {
		return err
	}

	return loginForDomainWith(domain, func() error {
		cmd := exec.Command(cfPath, "tunnel", "login")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	})
}

func loginForDomainWith(domain string, runLogin func() error) error {
	defaultCertPath := filepath.Join(ConfigDir(), "cert.pem")

	var backupPath string
	if _, err := os.Stat(defaultCertPath); err == nil {
		backupPath = defaultCertPath + ".backup"
		if err := os.Rename(defaultCertPath, backupPath); err != nil {
			return fmt.Errorf("failed to backup existing cert: %w", err)
		}
	}

	if err := runLogin(); err != nil {
		if backupPath != "" {
			_ = os.Rename(backupPath, defaultCertPath)
		}
		return err
	}

	domainCertPath := CertPathForDomain(domain)
	if err := os.Rename(defaultCertPath, domainCertPath); err != nil {
		return fmt.Errorf("failed to save domain cert: %w", err)
	}

	if backupPath != "" {
		_ = os.Rename(backupPath, defaultCertPath)
	}

	return nil
}

// certToken holds the decoded content of a cloudflared cert.pem
type certToken struct {
	ZoneID    string `json:"zoneID"`
	AccountID string `json:"accountID"`
	APIToken  string `json:"apiToken"`
}

// ParseCertZoneID extracts the zone ID from a cert.pem file.
func ParseCertZoneID(certPath string) (string, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}

	content := string(data)
	start := strings.Index(content, "-----BEGIN ARGO TUNNEL TOKEN-----")
	end := strings.Index(content, "-----END ARGO TUNNEL TOKEN-----")
	if start == -1 || end == -1 {
		return "", fmt.Errorf("invalid cert.pem format")
	}

	b64 := strings.TrimSpace(content[start+len("-----BEGIN ARGO TUNNEL TOKEN-----") : end])
	b64 = strings.ReplaceAll(b64, "\n", "")

	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("failed to decode cert: %w", err)
	}

	var token certToken
	if err := json.Unmarshal(decoded, &token); err != nil {
		return "", fmt.Errorf("failed to parse cert token: %w", err)
	}

	return token.ZoneID, nil
}

// TunnelExists checks whether a named tunnel already exists.
func TunnelExists(name string) bool {
	cfPath, err := ResolveCloudflared()
	if err != nil {
		return false
	}
	out, err := exec.Command(cfPath, "tunnel", "list", "-o", "json").CombinedOutput()
	if err != nil {
		return false
	}
	return parseTunnelListHasName(out, name)
}

func parseTunnelListHasName(data []byte, name string) bool {
	var tunnels []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &tunnels) != nil {
		return false
	}
	for _, t := range tunnels {
		if strings.EqualFold(t.Name, name) {
			return true
		}
	}
	return false
}

// CreateTunnel runs `cloudflared tunnel create <name>`.
func CreateTunnel(name string) error {
	cfPath, err := ResolveCloudflared()
	if err != nil {
		return err
	}
	cmd := exec.Command(cfPath, "tunnel", "create", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RouteDNS creates a CNAME record for hostname → tunnel.
func RouteDNS(tunnelName, hostname string) error {
	return RouteDNSWithCert(tunnelName, hostname, "")
}

// RouteDNSWithCert creates a CNAME record using a specific origin cert.
func RouteDNSWithCert(tunnelName, hostname, certPath string) error {
	cfPath, err := ResolveCloudflared()
	if err != nil {
		return err
	}

	args := []string{"tunnel"}
	if certPath != "" {
		args = append(args, "--origincert", certPath)
	}
	args = append(args, "route", "dns", "--overwrite-dns", tunnelName, hostname)

	cmd := exec.Command(cfPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// VerifyDNS checks if a hostname resolves (has DNS records).
// Returns true if the hostname resolves within the timeout.
func VerifyDNS(hostname string, timeout time.Duration) bool {
	return verifyDNSWith(hostname, timeout, net.LookupHost, 2*time.Second)
}

func verifyDNSWith(hostname string, timeout time.Duration, lookup func(string) ([]string, error), interval time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := lookup(hostname)
		if err == nil {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

// GetTunnelUUID returns the UUID for a named tunnel.
func GetTunnelUUID(tunnelName string) string {
	return lookupTunnelID(tunnelName)
}

// ConfigDir returns the path where gtl stores tunnel config files.
func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cloudflared")
}

// writeTunnelConfig generates a cloudflared config.yml for a named tunnel
// routing a single hostname to a local port.
func writeTunnelConfig(tunnelName, hostname string, port int) (string, error) {
	credPath := findCredentialsFile(tunnelName)

	config := fmt.Sprintf("tunnel: %q\ncredentials-file: %q\n\ningress:\n  - hostname: %q\n    service: http://localhost:%d\n  - service: http_status:404\n",
		tunnelName, credPath, hostname, port)

	dir := ConfigDir()
	configPath := filepath.Join(dir, fmt.Sprintf("gtl-%s.yml", tunnelName))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		return "", err
	}
	return configPath, nil
}

// findCredentialsFile locates the tunnel credentials JSON in ~/.cloudflared/
// by looking up the tunnel ID via `cloudflared tunnel list`.
func findCredentialsFile(tunnelName string) string {
	dir := ConfigDir()
	if id := lookupTunnelID(tunnelName); id != "" {
		path := filepath.Join(dir, id+".json")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join(dir, tunnelName+".json")
}

func lookupTunnelID(tunnelName string) string {
	cfPath, err := ResolveCloudflared()
	if err != nil {
		return ""
	}
	out, err := exec.Command(cfPath, "tunnel", "list", "-o", "json").CombinedOutput()
	if err != nil {
		return ""
	}
	return parseTunnelListID(out, tunnelName)
}

func parseTunnelListID(data []byte, name string) string {
	var tunnels []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &tunnels) != nil {
		return ""
	}
	for _, t := range tunnels {
		if strings.EqualFold(t.Name, name) {
			return t.ID
		}
	}
	return ""
}

// GenerateConfig returns the config YAML content as a string (for testing).
func GenerateConfig(tunnelName, hostname string, port int, credPath string) string {
	return fmt.Sprintf("tunnel: %q\ncredentials-file: %q\n\ningress:\n  - hostname: %q\n    service: http://localhost:%d\n  - service: http_status:404\n",
		tunnelName, credPath, hostname, port)
}

// WriteShareConfig writes a temporary cloudflared config for a share session.
// The config file is named distinctly (gtl-share-<name>.yml) so it never
// conflicts with the main tunnel config. Returns the path; caller is
// responsible for cleanup.
func WriteShareConfig(tunnelName, hostname string, port int) (string, error) {
	credPath := findCredentialsFile(tunnelName)

	config := fmt.Sprintf("tunnel: %q\ncredentials-file: %q\n\ningress:\n  - hostname: %q\n    service: http://localhost:%d\n  - service: http_status:404\n",
		tunnelName, credPath, hostname, port)

	dir := ConfigDir()
	configPath := filepath.Join(dir, fmt.Sprintf("gtl-share-%s.yml", tunnelName))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		return "", err
	}
	return configPath, nil
}

// IsTunnelRunning checks if a cloudflared process is already running for the
// given tunnel name by scanning for matching processes.
func IsTunnelRunning(tunnelName string) bool {
	out, err := exec.Command("pgrep", "-f", "cloudflared.*tunnel.*run.*"+tunnelName).Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}
