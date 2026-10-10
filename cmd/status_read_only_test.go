package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusReadOnlyReturnsRawAllocationWithoutStateChanges(t *testing.T) {
	f := newIntrospectionFixture(t, "project: fixture\n", false)
	if err := os.MkdirAll(filepath.Join(f.state, "probes"), 0o700); err != nil {
		t.Fatal(err)
	}
	registryJSON := `{"version":4,"allocations":[{"worktree":"` + f.work + `","project":"recorded","branch":"feature","ports":[0],"allocation_generation":"bfbf7a9c-f5df-4e33-9fd0-f0d0ca5b26d0","runtime_binding":{"kind":"never_started"},"host_metadata":"preserve-verbatim"}]}`
	writeIntrospectionFile(t, filepath.Join(f.state, "registry.json"), registryJSON)
	writeIntrospectionFile(t, filepath.Join(f.state, "config.json"), `{"router":{"port":43210},"sentinel":"preserve-verbatim"}`)
	writeIntrospectionFile(t, filepath.Join(f.state, "probes", "999999.pgids"), "sentinel process metadata\n")
	git := "#!/bin/sh\nif [ \"$1 $2 $3\" = \"rev-parse --abbrev-ref HEAD\" ]; then\n  printf observed-branch\nelif [ \"$1 $2 $3\" = \"remote get-url origin\" ]; then\n  printf git@github.com:fixture/repo.git\nelse\n  exit 1\nfi\n"
	if err := os.WriteFile(filepath.Join(f.bin, "git"), []byte(git), 0o700); err != nil {
		t.Fatal(err)
	}

	before := snapshotIntrospection(t, f.base)
	out, stderr, err := runIntrospection(t, f, "status", "--json", "--read-only")
	if err != nil {
		t.Fatalf("status --read-only: %v: %s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
	var allocations []map[string]any
	if err := json.Unmarshal([]byte(out), &allocations); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(allocations) != 1 {
		t.Fatalf("allocations = %s", out)
	}
	allocation := allocations[0]
	if allocation["allocation_generation"] != "bfbf7a9c-f5df-4e33-9fd0-f0d0ca5b26d0" {
		t.Fatalf("allocation_generation lost: %s", out)
	}
	binding, ok := allocation["runtime_binding"].(map[string]any)
	if !ok || binding["kind"] != "never_started" {
		t.Fatalf("runtime_binding lost: %s", out)
	}
	if allocation["host_metadata"] != "preserve-verbatim" {
		t.Fatalf("raw allocation field lost: %s", out)
	}
	if allocation["branch"] != "observed-branch" {
		t.Fatalf("live branch probe missing: %s", out)
	}
	assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
}

func TestStatusReadOnlyRequiresJSONBeforeEffects(t *testing.T) {
	f := newIntrospectionFixture(t, "", false)
	before := snapshotIntrospection(t, f.base)
	out, stderr, err := runIntrospection(t, f, "status", "--read-only")
	if err == nil {
		t.Fatalf("accepted --read-only without --json: %s %s", out, stderr)
	}
	if !strings.Contains(stderr, "--read-only requires status") {
		t.Fatalf("unexpected error: %s", stderr)
	}
	assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
}
