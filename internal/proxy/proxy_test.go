package proxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestProxyForwardsHTTP(t *testing.T) {
	targetPort := freePort(t)
	listenPort := freePort(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "world")
	})
	target := &http.Server{Addr: fmt.Sprintf(":%d", targetPort), Handler: mux}
	go func() { _ = target.ListenAndServe() }()
	defer target.Close()
	waitForPort(t, targetPort)

	go func() {
		_ = Run(Options{ListenPort: listenPort, TargetPort: targetPort})
	}()
	waitForPort(t, listenPort)

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/hello", listenPort))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "world" {
		t.Errorf("expected 'world', got %q", string(body))
	}
}

func TestProxyPreservesHostHeader(t *testing.T) {
	targetPort := freePort(t)
	listenPort := freePort(t)

	var receivedHost string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	})
	target := &http.Server{Addr: fmt.Sprintf(":%d", targetPort), Handler: mux}
	go func() { _ = target.ListenAndServe() }()
	defer target.Close()
	waitForPort(t, targetPort)

	go func() {
		_ = Run(Options{ListenPort: listenPort, TargetPort: targetPort})
	}()
	waitForPort(t, listenPort)

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://localhost:%d/", listenPort), nil)
	req.Host = "myapp.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if receivedHost != "myapp.localhost" {
		t.Errorf("expected host 'myapp.localhost', got %q", receivedHost)
	}
}

func TestProxyTLSTermination(t *testing.T) {
	targetPort := freePort(t)
	listenPort := freePort(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/secure", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	target := &http.Server{Addr: fmt.Sprintf(":%d", targetPort), Handler: mux}
	go func() { _ = target.ListenAndServe() }()
	defer target.Close()
	waitForPort(t, targetPort)

	go func() {
		_ = Run(Options{ListenPort: listenPort, TargetPort: targetPort, TLS: true})
	}()
	waitForPort(t, listenPort)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	resp, err := client.Get(fmt.Sprintf("https://localhost:%d/secure", listenPort))
	if err != nil {
		t.Fatalf("TLS request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("expected 'ok', got %q", string(body))
	}
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %d did not become available", port)
}
