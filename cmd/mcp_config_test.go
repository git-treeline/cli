package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestMcpConfigReturnsNoError(t *testing.T) {
	if err := mcpConfigCmd.RunE(mcpConfigCmd, nil); err != nil {
		t.Fatalf("mcp-config returned error: %v", err)
	}
}

func TestMcpConfigOutputContent(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	runErr := mcpConfigCmd.RunE(mcpConfigCmd, nil)

	_ = w.Close()
	os.Stdout = orig

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if runErr != nil {
		t.Fatalf("mcp-config failed: %v", runErr)
	}

	for _, want := range []string{"mcpServers", "Cursor", "Claude Code", "Tools provided"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q", want)
		}
	}
}
