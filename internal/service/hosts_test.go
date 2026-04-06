package service

import (
	"strings"
	"testing"
)

func TestBuildHostsBlock(t *testing.T) {
	block := buildHostsBlock([]string{"salt-main.localhost", "salt-feature.localhost"})
	if !strings.Contains(block, hostsBegin()) {
		t.Error("expected BEGIN marker")
	}
	if !strings.Contains(block, hostsEnd()) {
		t.Error("expected END marker")
	}
	if !strings.Contains(block, "127.0.0.1 salt-main.localhost") {
		t.Error("expected salt-main entry")
	}
	if !strings.Contains(block, "127.0.0.1 salt-feature.localhost") {
		t.Error("expected salt-feature entry")
	}
}

func TestReplaceHostsBlock_Insert(t *testing.T) {
	original := "127.0.0.1 localhost\n::1 localhost\n"
	block := buildHostsBlock([]string{"myapp.localhost"})
	result, err := replaceHostsBlock(original, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "127.0.0.1 localhost") {
		t.Error("original entries should be preserved")
	}
	if !strings.Contains(result, "127.0.0.1 myapp.localhost") {
		t.Error("new entry should be appended")
	}
}

func TestReplaceHostsBlock_Update(t *testing.T) {
	original := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 old-route.localhost\n" +
		hostsEnd() + "\n"
	block := buildHostsBlock([]string{"new-route.localhost"})
	result, err := replaceHostsBlock(original, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, "old-route") {
		t.Error("old entry should be removed")
	}
	if !strings.Contains(result, "new-route") {
		t.Error("new entry should be present")
	}
	if strings.Count(result, hostsBegin()) != 1 {
		t.Error("should have exactly one BEGIN marker")
	}
}

func TestReplaceHostsBlock_Remove(t *testing.T) {
	original := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 myapp.localhost\n" +
		hostsEnd() + "\n"
	result, err := replaceHostsBlock(original, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, hostsBegin()) {
		t.Error("managed block should be removed")
	}
	if !strings.Contains(result, "127.0.0.1 localhost") {
		t.Error("original entries should be preserved")
	}
}

func TestReplaceHostsBlock_MalformedMarkers(t *testing.T) {
	content := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 myapp.localhost\n"
	// No END marker — should return error, not truncate

	_, err := replaceHostsBlock(content, buildHostsBlock([]string{"new.localhost"}))
	if err == nil {
		t.Fatal("expected error for BEGIN without END marker")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("expected 'malformed' in error, got: %s", err)
	}

	// Same for removal
	_, err = replaceHostsBlock(content, "")
	if err == nil {
		t.Fatal("expected error for BEGIN without END on removal")
	}
}

func TestReplaceHostsBlock_EmptyBlock(t *testing.T) {
	content := "127.0.0.1 localhost\n"
	result, err := replaceHostsBlock(content, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != content {
		t.Errorf("removing non-existent block should be a no-op, got: %q", result)
	}
}

func TestReplaceHostsBlock_DuplicateMarkers(t *testing.T) {
	content := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 first.localhost\n" +
		hostsEnd() + "\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 second.localhost\n" +
		hostsEnd() + "\n"
	block := buildHostsBlock([]string{"new.localhost"})
	result, err := replaceHostsBlock(content, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "new.localhost") {
		t.Error("new entry should be present")
	}
	// First block is replaced; second remains
	if !strings.Contains(result, "second.localhost") {
		t.Error("second managed block should be preserved (only first is replaced)")
	}
}

func TestParseManagedHosts(t *testing.T) {
	content := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 salt-main.localhost\n" +
		"127.0.0.1 api-feature.localhost\n" +
		hostsEnd() + "\n"

	hosts := parseManagedHosts(content)
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d: %v", len(hosts), hosts)
	}
	if hosts[0] != "salt-main.localhost" || hosts[1] != "api-feature.localhost" {
		t.Errorf("unexpected hosts: %v", hosts)
	}
}

func TestParseManagedHosts_NoBlock(t *testing.T) {
	hosts := parseManagedHosts("127.0.0.1 localhost\n")
	if len(hosts) != 0 {
		t.Errorf("expected empty, got %v", hosts)
	}
}

func TestMissingHosts(t *testing.T) {
	// MissingHosts reads /etc/hosts which we can't control in unit tests.
	// Verify the logic: when managed is empty, all expected are missing.
	expected := []string{"a.localhost", "b.localhost"}
	missing := MissingHosts(expected)
	// On any system, either managed contains our entries or it doesn't.
	// At minimum, the function should not panic and should return a subset of expected.
	for _, h := range missing {
		found := false
		for _, e := range expected {
			if h == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("MissingHosts returned %q which is not in expected set", h)
		}
	}
}
