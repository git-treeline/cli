package registry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReleasePreflightRejectsUnknownOrAmbiguousEvidence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GTL_HOME", t.TempDir())
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"empty object": `{}`, "null": `null`, "array": `[]`, "corrupt": `{broken`,
		"future empty": `{"version":99,"allocations":[]}`, "zero version": `{"version":0,"allocations":[]}`,
		"null version": `{"version":null,"allocations":[]}`, "string version": `{"version":"2","allocations":[]}`,
		"missing allocations": `{"version":2}`, "null allocations": `{"version":2,"allocations":null}`,
		"object allocations": `{"version":2,"allocations":{}}`, "unknown envelope": `{"version":2,"allocations":[],"future":true}`,
		"null row": `{"allocations":[null]}`, "array row": `{"allocations":[[]]}`,
		"missing target": `{"allocations":[{}]}`, "numeric target": `{"allocations":[{"worktree":12}]}`,
		"relative target": `{"allocations":[{"worktree":"relative"}]}`, "nul target": `{"allocations":[{"worktree":"/tmp/fixture\u0000"}]}`,
		"duplicate target":       `{"allocations":[{"worktree":"/tmp/fixture"},{"worktree":"/tmp/fixture"}]}`,
		"duplicate alias":        fmt.Sprintf(`{"allocations":[{"worktree":%q},{"worktree":%q}]}`, root, alias),
		"duplicate envelope key": `{"allocations":[{"worktree":"/tmp/fixture"}],"allocations":[]}`,
		"duplicate target key":   `{"allocations":[{"worktree":"/tmp/first","worktree":"/tmp/second"}]}`,
		"invalid edges":          `{"version":2,"allocations":[],"edges":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			reg := New(filepath.Join(t.TempDir(), "registry.json"))
			before := []byte(source)
			if err := os.WriteFile(reg.Path, before, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := reg.ReleasePreflight(); err == nil {
				t.Fatal("invalid registry passed release preflight")
			}
			if _, err := reg.FindForRelease(root); err == nil {
				t.Fatal("invalid registry became an unallocated target")
			}
			after, err := os.ReadFile(reg.Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("refusal changed evidence: %v", err)
			}
			if _, err := os.Stat(reg.Path + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("read-only query created lock state: %v", err)
			}
		})
	}
}

func TestReleasePreflightPreservesKnownLegacyEnvelopes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for name, prefix := range map[string]string{"versionless": "", "v1": `"version":1,`, "v2": `"version":2,`} {
		t.Run(name, func(t *testing.T) {
			reg := New(filepath.Join(t.TempDir(), "registry.json"))
			before := []byte(`{` + prefix + `"allocations":[{"worktree":"/tmp/fixture","project":"fixture","port":3010,"links":{"api":"branch"}}]}`)
			if err := os.WriteFile(reg.Path, before, 0o600); err != nil {
				t.Fatal(err)
			}
			alloc, err := reg.FindForRelease("/tmp/fixture")
			if err != nil || alloc == nil || GetString(alloc, "project") != "fixture" {
				t.Fatalf("legacy release query: %v, %v", alloc, err)
			}
			after, err := os.ReadFile(reg.Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("legacy read wrote migration: %v", err)
			}
		})
	}
}

func TestReleasePreflightMissingRegistryAndOrdinaryReaderRemainDistinct(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	reg := New(filepath.Join(t.TempDir(), "missing.json"))
	if alloc, err := reg.FindForRelease("/tmp/fixture"); err != nil || alloc != nil {
		t.Fatalf("missing registry should permit unallocated target: %v, %v", alloc, err)
	}
	if _, err := os.Stat(reg.Path); !os.IsNotExist(err) {
		t.Fatalf("query created registry: %v", err)
	}
	if err := os.WriteFile(reg.Path, []byte(`{"version":99,"allocations":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.FindStrict("/tmp/fixture"); err != nil {
		t.Fatalf("ordinary read compatibility changed: %v", err)
	}
	if _, err := reg.FindForRelease("/tmp/fixture"); err == nil {
		t.Fatal("future registry accepted for destructive release")
	}
}
