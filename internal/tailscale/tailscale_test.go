package tailscale

import (
	"fmt"
	"os"
	"testing"
)

func stubResolve(t *testing.T, path string) {
	t.Helper()
	origLook := lookPath
	origStat := statFile
	t.Cleanup(func() { lookPath = origLook; statFile = origStat })
	lookPath = func(name string) (string, error) { return path, nil }
	statFile = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
}

func stubCmdOut(t *testing.T, fn func(string, ...string) ([]byte, error)) {
	t.Helper()
	orig := cmdOut
	t.Cleanup(func() { cmdOut = orig })
	cmdOut = fn
}

func stubRunCmd(t *testing.T, fn func(string, ...string) ([]byte, []byte, error)) {
	t.Helper()
	orig := runCmd
	t.Cleanup(func() { runCmd = orig })
	runCmd = fn
}

// --- ResolveTailscale ---

func TestResolveTailscale_PATH(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(name string) (string, error) { return "/usr/bin/tailscale", nil }

	path, err := ResolveTailscale()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/usr/bin/tailscale" {
		t.Errorf("got %q, want /usr/bin/tailscale", path)
	}
}

func TestResolveTailscale_MacApp(t *testing.T) {
	origLook := lookPath
	origStat := statFile
	t.Cleanup(func() { lookPath = origLook; statFile = origStat })

	lookPath = func(name string) (string, error) { return "", fmt.Errorf("not found") }
	statFile = func(name string) (os.FileInfo, error) {
		if name == macAppPath {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}

	path, err := ResolveTailscale()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != macAppPath {
		t.Errorf("got %q, want %q", path, macAppPath)
	}
}

func TestResolveTailscale_NotFound(t *testing.T) {
	origLook := lookPath
	origStat := statFile
	t.Cleanup(func() { lookPath = origLook; statFile = origStat })

	lookPath = func(name string) (string, error) { return "", fmt.Errorf("not found") }
	statFile = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	_, err := ResolveTailscale()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- ParseStatus ---

func TestParseStatus_Online(t *testing.T) {
	data := []byte(`{"Self":{"DNSName":"macbook.tail1234.ts.net.","Online":true}}`)
	name, online, err := ParseStatus(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "macbook.tail1234.ts.net" {
		t.Errorf("got name %q, want macbook.tail1234.ts.net", name)
	}
	if !online {
		t.Error("expected online=true")
	}
}

func TestParseStatus_Offline(t *testing.T) {
	data := []byte(`{"Self":{"DNSName":"macbook.tail1234.ts.net.","Online":false}}`)
	_, online, err := ParseStatus(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if online {
		t.Error("expected online=false")
	}
}

func TestParseStatus_BadJSON(t *testing.T) {
	_, _, err := ParseStatus([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

// --- Preflight ---

func TestPreflight_Success(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubCmdOut(t, func(name string, args ...string) ([]byte, error) {
		return []byte(`{"Self":{"DNSName":"dev.mynet.ts.net.","Online":true}}`), nil
	})

	name, err := Preflight()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "dev.mynet.ts.net" {
		t.Errorf("got %q, want dev.mynet.ts.net", name)
	}
}

func TestPreflight_DaemonNotRunning(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubCmdOut(t, func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("exit status 1")
	})

	_, err := Preflight()
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "daemon is not running") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPreflight_NotConnected(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubCmdOut(t, func(name string, args ...string) ([]byte, error) {
		return []byte(`{"Self":{"DNSName":"dev.ts.net.","Online":false}}`), nil
	})

	_, err := Preflight()
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPreflight_EmptyHostname(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubCmdOut(t, func(name string, args ...string) ([]byte, error) {
		return []byte(`{"Self":{"DNSName":"","Online":true}}`), nil
	})

	_, err := Preflight()
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "empty hostname") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPreflight_NoTailscale(t *testing.T) {
	origLook := lookPath
	origStat := statFile
	t.Cleanup(func() { lookPath = origLook; statFile = origStat })
	lookPath = func(name string) (string, error) { return "", fmt.Errorf("not found") }
	statFile = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	_, err := Preflight()
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- IsRunning ---

func TestIsRunning_Online(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubCmdOut(t, func(name string, args ...string) ([]byte, error) {
		return []byte(`{"Self":{"DNSName":"dev.ts.net.","Online":true}}`), nil
	})

	if !IsRunning() {
		t.Error("expected IsRunning=true")
	}
}

func TestIsRunning_Offline(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubCmdOut(t, func(name string, args ...string) ([]byte, error) {
		return []byte(`{"Self":{"DNSName":"dev.ts.net.","Online":false}}`), nil
	})

	if IsRunning() {
		t.Error("expected IsRunning=false")
	}
}

func TestIsRunning_NoTailscale(t *testing.T) {
	origLook := lookPath
	origStat := statFile
	t.Cleanup(func() { lookPath = origLook; statFile = origStat })
	lookPath = func(name string) (string, error) { return "", fmt.Errorf("not found") }
	statFile = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	if IsRunning() {
		t.Error("expected IsRunning=false when tailscale not installed")
	}
}

// --- GetDNSName ---

func TestGetDNSName(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubCmdOut(t, func(name string, args ...string) ([]byte, error) {
		return []byte(`{"Self":{"DNSName":"laptop.mynet.ts.net.","Online":true}}`), nil
	})

	name, err := GetDNSName()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "laptop.mynet.ts.net" {
		t.Errorf("got %q, want laptop.mynet.ts.net", name)
	}
}

func TestGetDNSName_Empty(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubCmdOut(t, func(name string, args ...string) ([]byte, error) {
		return []byte(`{"Self":{"DNSName":"","Online":true}}`), nil
	})

	_, err := GetDNSName()
	if err == nil {
		t.Fatal("expected error for empty DNS name")
	}
}

// --- Serve ---

func TestServe_Success(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	var gotArgs []string
	stubRunCmd(t, func(name string, args ...string) ([]byte, []byte, error) {
		gotArgs = append([]string{name}, args...)
		return []byte("Serve started"), nil, nil
	})

	if err := Serve(3010); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotArgs) != 4 || gotArgs[3] != "https+insecure://localhost:3010" {
		t.Errorf("unexpected args: %v", gotArgs)
	}
}

func TestServe_NotEnabled(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubRunCmd(t, func(name string, args ...string) ([]byte, []byte, error) {
		msg := "Serve is not enabled on your tailnet.\nTo enable, visit:\n\n         https://login.tailscale.com/f/serve?node=abc123\n"
		return []byte(msg), nil, fmt.Errorf("exit status 1")
	})

	err := Serve(3010)
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if !contains(got, "not enabled") {
		t.Errorf("expected 'not enabled' hint, got: %v", got)
	}
	if !contains(got, "login.tailscale.com") {
		t.Errorf("expected enable URL in error, got: %v", got)
	}
}

func TestServe_NotLoggedIn(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	stubRunCmd(t, func(name string, args ...string) ([]byte, []byte, error) {
		return nil, []byte("not logged in"), fmt.Errorf("exit status 1")
	})

	err := Serve(3010)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "not logged in") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- ServeOff ---

func TestServeOff(t *testing.T) {
	stubResolve(t, "/usr/bin/tailscale")
	var called bool
	stubRunCmd(t, func(name string, args ...string) ([]byte, []byte, error) {
		called = true
		if len(args) != 2 || args[0] != "serve" || args[1] != "off" {
			t.Errorf("unexpected args: %v", args)
		}
		return nil, nil, nil
	})

	if err := ServeOff(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected runCmd to be called")
	}
}

// --- extractEnableURL ---

func TestExtractEnableURL(t *testing.T) {
	input := "Serve is not enabled.\nTo enable, visit:\n\n         https://login.tailscale.com/f/serve?node=abc\n"
	got := extractEnableURL(input)
	if got != "https://login.tailscale.com/f/serve?node=abc" {
		t.Errorf("got %q", got)
	}
}

func TestExtractEnableURL_NoURL(t *testing.T) {
	got := extractEnableURL("some other error message")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
