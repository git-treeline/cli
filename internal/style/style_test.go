package style

import (
	"regexp"
	"strings"
	"testing"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

func assertPlain(t *testing.T, styled, expected string) {
	t.Helper()
	plain := stripANSI(styled)
	if !strings.Contains(plain, expected) {
		t.Errorf("expected %q in output, got plain: %q (raw: %q)", expected, plain, styled)
	}
}

func TestActionf_ContainsPrefix(t *testing.T) {
	got := Actionf("installing %s", "pkg")
	assertPlain(t, got, "==>")
	assertPlain(t, got, "installing pkg")
}

func TestActionf_NoArgs(t *testing.T) {
	got := Actionf("plain message")
	assertPlain(t, got, "==>")
	assertPlain(t, got, "plain message")
}

func TestWarnf_ContainsWarningPrefix(t *testing.T) {
	got := Warnf("something went wrong: %s", "disk full")
	assertPlain(t, got, "Warning:")
	assertPlain(t, got, "something went wrong: disk full")
}

func TestWarnf_NoArgs(t *testing.T) {
	got := Warnf("simple warning")
	assertPlain(t, got, "Warning:")
	assertPlain(t, got, "simple warning")
}

func TestErrf_ContainsErrorPrefix(t *testing.T) {
	got := Errf("failed: %d", 42)
	assertPlain(t, got, "Error:")
	assertPlain(t, got, "failed: 42")
}

func TestErrf_NoArgs(t *testing.T) {
	got := Errf("simple error")
	assertPlain(t, got, "Error:")
	assertPlain(t, got, "simple error")
}

func TestSuccessf(t *testing.T) {
	got := Successf("Done! %s", "ready")
	assertPlain(t, got, "Done! ready")
}

func TestDimf(t *testing.T) {
	got := Dimf("port: %d", 3000)
	assertPlain(t, got, "port: 3000")
}

func TestCmd(t *testing.T) {
	got := Cmd("gtl setup")
	assertPlain(t, got, "gtl setup")
}

func TestLink(t *testing.T) {
	got := Link("https://example.com")
	assertPlain(t, got, "https://example.com")
}

func TestActionPrefix(t *testing.T) {
	got := ActionPrefix()
	assertPlain(t, got, "==>")
}

func TestActionf_SpecialCharsInMessage(t *testing.T) {
	got := Actionf("file: %s", "path/with spaces/and%percent")
	assertPlain(t, got, "path/with spaces/and%percent")
}

func TestWarnf_PrefixIsSeparateFromMessage(t *testing.T) {
	got := Warnf("disk full")
	plain := stripANSI(got)
	if !strings.HasPrefix(plain, "Warning: ") {
		t.Errorf("expected 'Warning: ' prefix in %q", plain)
	}
}

func TestErrf_PrefixIsSeparateFromMessage(t *testing.T) {
	got := Errf("timeout")
	plain := stripANSI(got)
	if !strings.HasPrefix(plain, "Error: ") {
		t.Errorf("expected 'Error: ' prefix in %q", plain)
	}
}

func TestActionf_PrefixIsSeparateFromMessage(t *testing.T) {
	got := Actionf("running setup")
	plain := stripANSI(got)
	if !strings.HasPrefix(plain, "==> ") {
		t.Errorf("expected '==> ' prefix in %q", plain)
	}
}
