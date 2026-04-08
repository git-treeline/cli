package cmd

import "testing"

func TestClassifyPortConfig_Conflict(t *testing.T) {
	got := classifyPortConfig(8443, 8443)
	if got != "conflict" {
		t.Errorf("classifyPortConfig(8443, 8443) = %q, want %q", got, "conflict")
	}
}

func TestClassifyPortConfig_CommonDevPort(t *testing.T) {
	got := classifyPortConfig(3000, 8443)
	if got != "common_dev_port" {
		t.Errorf("classifyPortConfig(3000, 8443) = %q, want %q", got, "common_dev_port")
	}
}

func TestClassifyPortConfig_Ok(t *testing.T) {
	got := classifyPortConfig(3002, 8443)
	if got != "" {
		t.Errorf("classifyPortConfig(3002, 8443) = %q, want empty", got)
	}
}

func TestClassifyPortConfig_ConflictWhenBaseEqualsRouter(t *testing.T) {
	// Even when base is also a common dev port, equality with router is the conflict
	got := classifyPortConfig(3000, 3000)
	if got != "conflict" {
		t.Errorf("classifyPortConfig(3000, 3000) = %q, want %q", got, "conflict")
	}
}
