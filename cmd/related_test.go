package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func relatedFixture(t *testing.T, content string, allocated bool) introspectionFixture {
	t.Helper()
	f := newIntrospectionFixture(t, content, allocated)
	git := `#!/bin/sh
case "$*" in
  'remote get-url origin')
    case "$PWD" in */api-feature) printf 'git@github.com:acme/api.git\n';; *) printf 'git@github.com:acme/web.git\n';; esac ;;
  'rev-parse --show-toplevel') printf '%s\n' "$PWD" ;;
  'rev-parse --abbrev-ref HEAD') printf 'main\n' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(f.bin, "git"), []byte(git), 0700); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRelatedJSONReadOnlyLegacyAndMissingState(t *testing.T) {
	for name, legacy := range map[string]string{
		"database": "database:\n  pattern: legacy_{worktree}\n",
		"branch":   "default_branch: release\n",
		"commands": "setup_commands: [touch sentinel]\nstart_command: touch sentinel\n",
		"env":      "env_file:\n  target: .env.test\n  source: .env.seed\n",
		"editor":   "editor:\n  vscode_title: Legacy\n",
		"ports":    "ports_needed: 4\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := relatedFixture(t, "project: fixture\n"+legacy, false)
			before := snapshotIntrospection(t, f.base)
			out, stderr, err := runIntrospection(t, f, "related", "--json")
			if err != nil || strings.TrimSpace(out) != "[]" || stderr != "" {
				t.Fatalf("related: out=%q stderr=%q err=%v", out, stderr, err)
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
			if _, err := os.Stat(f.state); !os.IsNotExist(err) {
				t.Fatalf("read created GTL_HOME: %v", err)
			}
		})
	}
}

func TestRelatedPreservesLiveDanglingConfigAndHumanOutput(t *testing.T) {
	f := relatedFixture(t, "project: fixture\nrelated_repos:\n  - repo: acme/api\n    type: configured-type\n  - acme/docs\n", true)
	live := filepath.Join(f.base, "api-feature")
	if err := os.Mkdir(live, 0755); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{
		"version": 2,
		"allocations": []any{
			map[string]any{"worktree": f.work, "branch": "main", "links": map[string]string{"api": "preview"}},
			map[string]any{"worktree": live, "branch": "feature"},
		},
		"edges": []any{
			map[string]any{"a": map[string]string{"repo": "acme/web", "branch": "main"}, "b": map[string]string{"repo": "acme/api", "branch": "feature"}, "createdAt": "2026-09-01T12:00:00Z"},
			map[string]any{"a": map[string]string{"repo": "acme/web", "branch": "main"}, "b": map[string]string{"repo": "acme/mobile", "branch": "spec"}, "type": "consumes-api", "createdAt": "2026-09-02T12:00:00Z"},
		},
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	writeIntrospectionFile(t, filepath.Join(f.state, "registry.json"), string(raw))
	before := snapshotIntrospection(t, f.base)
	out, stderr, err := runIntrospection(t, f, "related", "--json")
	if err != nil || stderr != "" {
		t.Fatalf("related: out=%q stderr=%q err=%v", out, stderr, err)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil || len(entries) != 3 {
		t.Fatalf("entries=%s err=%v", out, err)
	}
	byRepo := map[string]map[string]any{}
	for _, entry := range entries {
		byRepo[entry["repo"].(string)] = entry
	}
	api := byRepo["acme/api"]
	if api["path"] != live || api["branch"] != "feature" || api["source"] != "edge" || api["type"] != "related" || api["dangling"] != false || api["createdAt"] != "2026-09-01T12:00:00Z" {
		t.Fatalf("live edge changed: %v", api)
	}
	mobile := byRepo["acme/mobile"]
	if mobile["path"] != nil || mobile["dangling"] != true || mobile["type"] != "consumes-api" {
		t.Fatalf("dangling edge changed: %v", mobile)
	}
	docs := byRepo["acme/docs"]
	if docs["path"] != nil || docs["branch"] != "" || docs["source"] != "config" || docs["createdAt"] != nil || docs["dangling"] != true {
		t.Fatalf("config sibling changed: %v", docs)
	}
	assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
	out, _, err = runIntrospection(t, f, "related")
	if err != nil || !strings.Contains(out, "acme/api#feature  [edge/related]  "+live) || !strings.Contains(out, "acme/docs#  [config/related]  (not checked out)") {
		t.Fatalf("human output changed: %q err=%v", out, err)
	}
}

func TestRelatedJSONRejectsMalformedConfigAndRegistryWithoutWrites(t *testing.T) {
	for name, content := range map[string]string{
		"yaml": "project: [\n", "invalid project": "project: invalid-project\n",
		"related object": "related_repos: {repo: acme/api}\n", "related number": "related_repos: [7]\n",
		"missing repo": "related_repos: [{type: api}]\n", "empty repo": "related_repos: ['']\n",
		"invalid type": "related_repos: [{repo: acme/api, type: []}]\n",
	} {
		t.Run("config/"+name, func(t *testing.T) {
			f := relatedFixture(t, content, true)
			before := snapshotIntrospection(t, f.base)
			out, _, err := runIntrospection(t, f, "related", "--json")
			if err == nil || strings.TrimSpace(out) != "" {
				t.Fatalf("malformed config became success: out=%q err=%v", out, err)
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
	for name, registry := range map[string]string{
		"syntax": "{broken", "empty envelope": "{}", "missing allocations": `{"version":2}`,
		"null allocations": `{"version":2,"allocations":null}`, "invalid edges": `{"version":2,"allocations":[],"edges":false}`,
		"empty endpoint": `{"version":2,"allocations":[],"edges":[{}]}`,
		"invalid branch": `{"version":2,"allocations":[{"worktree":"/fixture","branch":7}]}`,
		"future schema":  `{"version":99,"allocations":[]}`,
	} {
		t.Run("registry/"+name, func(t *testing.T) {
			f := relatedFixture(t, "project: fixture\n", true)
			writeIntrospectionFile(t, filepath.Join(f.state, "registry.json"), registry)
			before := snapshotIntrospection(t, f.base)
			out, _, err := runIntrospection(t, f, "related", "--json")
			if err == nil || strings.TrimSpace(out) != "" {
				t.Fatalf("malformed registry became success: out=%q err=%v", out, err)
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
}
