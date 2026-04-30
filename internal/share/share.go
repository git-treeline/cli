// Package share provides private branch sharing via a token-gated reverse
// proxy fronting a cloudflared tunnel, or via Tailscale Serve for
// tailnet-only access. Tokens are ephemeral — generated per session and
// invalidated when the tunnel shuts down.
package share

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/git-treeline/cli/internal/tailscale"
	"github.com/git-treeline/cli/internal/tunnel"
)

const (
	cookieName = "gtl_share"
	pathPrefix = "/s/"
)

// GenerateToken returns a cryptographically random 32-character hex token.
func GenerateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// generateShortID returns an 8-character hex string for share subdomains.
func generateShortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// cookieValue computes an HMAC-SHA256 of the token so the raw token is never
// stored in the cookie.
func cookieValue(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("gtl_share"))
	return hex.EncodeToString(mac.Sum(nil))
}

// NewTokenHandler returns an http.Handler that gates access behind a secret
// token path. First visit to /s/<token> sets an auth cookie and redirects to
// /. Subsequent requests are authenticated via the cookie.
func NewTokenHandler(token string, appPort int) http.Handler {
	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", appPort),
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Header.Set("X-Forwarded-Host", r.In.Host)
		},
	}

	expectedCookie := cookieValue(token)
	tokenPath := pathPrefix + token

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, tokenPath) {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName,
				Value:    expectedCookie,
				Path:     "/",
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteStrictMode,
			})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		c, err := r.Cookie(cookieName)
		if err == nil && c.Value == expectedCookie {
			rp.ServeHTTP(w, r)
			return
		}

		http.NotFound(w, r)
	})
}

// freePort asks the OS for an available TCP port.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

// Run starts a private share session: token proxy + cloudflared tunnel.
// If tunnelName and domain are non-empty, uses a named tunnel with a random
// subdomain; otherwise falls back to a quick tunnel. Blocks until signal.
func Run(appPort int, tunnelName, domain string) error {
	cfPath, err := tunnel.ResolveCloudflared()
	if err != nil {
		return err
	}

	token := GenerateToken()
	proxyPort, err := freePort()
	if err != nil {
		return fmt.Errorf("could not find a free port for the share proxy: %w", err)
	}

	handler := NewTokenHandler(token, appPort)
	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", proxyPort),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "share proxy error: %v\n", err)
		}
	}()

	useNamed := tunnelName != "" && domain != ""
	if useNamed && tunnel.IsTunnelRunning(tunnelName) {
		fmt.Fprintf(os.Stderr, "Note: named tunnel %q is already running. Using quick tunnel instead.\n\n", tunnelName)
		useNamed = false
	}

	var cmd *exec.Cmd
	var configPath string

	if useNamed {
		routeKey := "s-" + generateShortID()
		hostname := routeKey + "." + domain

		var writeErr error
		configPath, writeErr = tunnel.WriteShareConfig(tunnelName, hostname, proxyPort)
		if writeErr != nil {
			return fmt.Errorf("failed to write share tunnel config: %w", writeErr)
		}

		cmd = exec.Command(cfPath, "tunnel", "--config", configPath, "run", tunnelName)
		cmd.Stdout = os.Stdout
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		stderrPipe, pipeErr := cmd.StderrPipe()
		if pipeErr != nil {
			return pipeErr
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start cloudflared: %w", err)
		}

		shareURL := fmt.Sprintf("https://%s%s%s", hostname, pathPrefix, token)
		fmt.Printf("Share URL: %s\n", shareURL)
		fmt.Printf("→ http://localhost:%d\n", appPort)
		fmt.Println()
		fmt.Println("Press Ctrl+C to stop sharing.")

		go tunnel.FilterCloudflaredLogs(stderrPipe)
	} else {
		cmd = exec.Command(cfPath, "tunnel", "--url", fmt.Sprintf("http://localhost:%d", proxyPort))
		cmd.Stdout = os.Stdout
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		stderrPipe, pipeErr := cmd.StderrPipe()
		if pipeErr != nil {
			return pipeErr
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start cloudflared: %w", err)
		}

		go scanForURL(stderrPipe, token, appPort)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	cleanup := func() {
		_ = server.Close()
		if configPath != "" {
			_ = os.Remove(configPath)
		}
	}

	select {
	case <-quit:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		cleanup()
		return <-done
	case err := <-done:
		cleanup()
		return err
	}
}

// RunTailscale exposes a local port on the tailnet via tailscale serve.
// No token proxy needed — Tailscale handles identity-based auth. Only people
// on the tailnet can reach the URL. Blocks until SIGINT/SIGTERM.
func RunTailscale(appPort int) error {
	dnsName, err := tailscale.Preflight()
	if err != nil {
		return err
	}

	if err := tailscale.Serve(appPort); err != nil {
		return err
	}

	// Ensure cleanup runs even if we're killed abruptly. Reset the signal
	// handler so the first Ctrl+C runs our cleanup instead of terminating.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("\nShare URL: https://%s\n", dnsName)
	fmt.Printf("→ http://localhost:%d\n", appPort)
	fmt.Println()
	fmt.Println("Only accessible to people on your tailnet.")
	fmt.Println("Press Ctrl+C to stop sharing.")

	<-quit
	signal.Stop(quit)

	fmt.Println("\nStopping tailscale serve...")
	if err := tailscale.ServeOff(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to clean up tailscale serve: %v\n", err)
		fmt.Fprintln(os.Stderr, "  Run 'tailscale serve off' manually to stop serving.")
	}
	return nil
}

func scanForURL(r io.Reader, token string, appPort int) {
	scanner := bufio.NewScanner(r)
	urlPrinted := false
	for scanner.Scan() {
		line := scanner.Text()
		if !urlPrinted {
			if u := tunnel.ExtractTrycloudflareURL(line); u != "" {
				fmt.Printf("Share URL: %s%s%s\n", u, pathPrefix, token)
				fmt.Printf("→ http://localhost:%d\n", appPort)
				fmt.Println()
				fmt.Println("Press Ctrl+C to stop sharing.")
				urlPrinted = true
				continue
			}
		}
		tunnel.FilterLine(line)
	}
}
