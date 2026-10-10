package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAptInstallDisallowsRemoval(t *testing.T) {
	bin := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GTL_TEST_SUDO_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GTL_TEST_SUDO_ARGS", argsFile)
	if err := aptInstall([]string{"libstdc++6", "postgresql-client:amd64=16.4-1.pgdg120+1"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := []string{"apt-get", "install", "-y", "--no-remove", "--", "libstdc++6", "postgresql-client:amd64=16.4-1.pgdg120+1"}
	if !slices.Equal(got, want) {
		t.Fatalf("sudo arguments = %q, want %q", got, want)
	}
}
