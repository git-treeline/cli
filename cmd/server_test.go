package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs fn and returns everything written to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w

	fn()

	_ = w.Close()
	os.Stderr = orig

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestWarnPortWiring_ViteWithoutPort(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte("export default {}"), 0644)

	out := captureStderr(t, func() { warnPortWiring("npm run dev", dir) })

	if !strings.Contains(out, "Port wiring") {
		t.Errorf("expected port wiring warning for Vite, got: %q", out)
	}
	if !strings.Contains(out, "Vite ignores") {
		t.Errorf("expected Vite-specific hint text, got: %q", out)
	}
	if !strings.Contains(out, "gtl doctor") {
		t.Errorf("expected pointer to gtl doctor, got: %q", out)
	}
}

func TestWarnPortWiring_NextJSWithoutPort(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "next.config.js"), []byte("module.exports = {}"), 0644)

	out := captureStderr(t, func() { warnPortWiring("npm run dev", dir) })

	if !strings.Contains(out, "Port wiring") {
		t.Errorf("expected port wiring warning for Next.js, got: %q", out)
	}
	if !strings.Contains(out, "Next.js reads PORT") {
		t.Errorf("expected Next.js-specific hint text, got: %q", out)
	}
}

func TestWarnPortWiring_DjangoWithoutPort(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "manage.py"), []byte("#!/usr/bin/env python"), 0644)

	out := captureStderr(t, func() { warnPortWiring("python manage.py runserver", dir) })

	if !strings.Contains(out, "Port wiring") {
		t.Errorf("expected port wiring warning for Django, got: %q", out)
	}
}

func TestWarnPortWiring_ViteWithPort(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte("export default {}"), 0644)

	out := captureStderr(t, func() { warnPortWiring("npx vite --port {port}", dir) })

	if out != "" {
		t.Errorf("expected no warning when {port} is present, got: %q", out)
	}
}

func TestWarnPortWiring_GoProject(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	out := captureStderr(t, func() { warnPortWiring("go run .", dir) })

	if out != "" {
		t.Errorf("expected no warning for Go (no PortHint), got: %q", out)
	}
}

func TestWarnPortWiring_RailsProject(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("source 'https://rubygems.org'"), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "config"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "config", "application.rb"), []byte(""), 0644)

	out := captureStderr(t, func() { warnPortWiring("bin/rails server", dir) })

	if out != "" {
		t.Errorf("expected no warning for Rails (reads PORT natively), got: %q", out)
	}
}

func TestWarnPortWiring_UnknownFramework(t *testing.T) {
	dir := t.TempDir()

	out := captureStderr(t, func() { warnPortWiring("./my-server", dir) })

	if out != "" {
		t.Errorf("expected no warning for unknown framework, got: %q", out)
	}
}

func TestInterpolateCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		port int
		want string
	}{
		{"no tokens", "npm run dev", 3000, "npm run dev"},
		{"single port", "npx vite --port {port} --host", 3000, "npx vite --port 3000 --host"},
		{"django", "python manage.py runserver 0.0.0.0:{port}", 8000, "python manage.py runserver 0.0.0.0:8000"},
		{"port_2", "cmd --port {port} --ws {port_2}", 3000, "cmd --port 3000 --ws 3001"},
		{"port_3", "cmd {port} {port_2} {port_3}", 5000, "cmd 5000 5001 5002"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpolateCommand(tt.cmd, tt.port)
			if got != tt.want {
				t.Errorf("interpolateCommand(%q, %d) = %q, want %q", tt.cmd, tt.port, got, tt.want)
			}
		})
	}
}
