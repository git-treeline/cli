// Package proxy provides a local reverse proxy for forwarding traffic from a
// stable listen port to a worktree's dynamically allocated port. Supports
// optional TLS termination via mkcert or self-signed certificates.
package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Options configures the reverse proxy.
type Options struct {
	ListenPort int
	TargetPort int
	TLS        bool
}

// Run starts a reverse proxy on ListenPort forwarding all traffic to
// TargetPort. It blocks until interrupted by SIGINT/SIGTERM and then
// performs a graceful shutdown.
func Run(opts Options) error {
	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", opts.TargetPort),
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			if opts.TLS {
				pr.Out.Header.Set("X-Forwarded-Proto", "https")
			}
		},
	}

	addr := fmt.Sprintf("127.0.0.1:%d", opts.ListenPort)
	server := &http.Server{
		Addr:              addr,
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d is already in use", opts.ListenPort)
	}

	scheme := "http"
	if opts.TLS {
		cert, certErr := resolveCert()
		if certErr != nil {
			_ = ln.Close()
			return fmt.Errorf("TLS setup failed: %w", certErr)
		}
		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{*cert},
			MinVersion:   tls.VersionTLS12,
		}
		ln = tls.NewListener(ln, server.TLSConfig)
		scheme = "https"
	}

	fmt.Printf("Proxying %s://localhost:%d → http://localhost:%d\n", scheme, opts.ListenPort, opts.TargetPort)
	fmt.Println("Press Ctrl+C to stop")

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		fmt.Printf("\nReceived %s, shutting down...\n", sig)
	case err := <-errCh:
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	fmt.Println("Proxy stopped.")
	return nil
}
