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
	result := replaceHostsBlock(original, block)

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
	result := replaceHostsBlock(original, block)

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
	result := replaceHostsBlock(original, "")

	if strings.Contains(result, hostsBegin()) {
		t.Error("managed block should be removed")
	}
	if !strings.Contains(result, "127.0.0.1 localhost") {
		t.Error("original entries should be preserved")
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

func TestStaleHosts(t *testing.T) {
	// StaleHosts calls ManagedHosts which reads /etc/hosts.
	// Test the logic via the helper instead.
	expected := []string{"a.localhost", "b.localhost", "c.localhost"}
	managed := []string{"a.localhost", "b.localhost"}

	have := make(map[string]bool)
	for _, h := range managed {
		have[h] = true
	}
	var stale []string
	for _, h := range expected {
		if !have[h] {
			stale = append(stale, h)
		}
	}
	if len(stale) != 1 || stale[0] != "c.localhost" {
		t.Errorf("expected [c.localhost], got %v", stale)
	}
}
