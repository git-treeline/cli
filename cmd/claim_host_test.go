package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/platform"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/spf13/cobra"
)

const claimHostTestID = "11111111-1111-4111-8111-111111111111"

func hostClaimInspect(t *testing.T, root, branch string) hostClaimRequest {
	t.Helper()
	result, err := inspectHostClaim(context.Background(), root, branch, claimHostTestID)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func hostClaimExecute(t *testing.T, root string, request hostClaimRequest) hostClaimReceipt {
	t.Helper()
	chdir(t, root)
	command := &cobra.Command{}
	command.SetContext(context.Background())
	return executeHostClaim(command, root, request)
}
func hostClaimBranchFixture(t *testing.T) (string, string) {
	t.Helper()
	root, seed := claimFixture(t)
	runGit(t, seed, "checkout", "-b", "feature/test")
	runGit(t, seed, "commit", "--allow-empty", "-m", "feature")
	runGit(t, seed, "push", "origin", "feature/test")
	return root, seed
}
func TestHostClaimInspectionReadOnly(t *testing.T) {
	root, _ := hostClaimBranchFixture(t)
	raw := []byte("project: fixture\nports_needed: 1\n")
	file := filepath.Join(root, config.ProjectConfigFile)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("GTL_HOME", missing)
	request := hostClaimInspect(t, root, "feature/test")
	if request.Target.Existing != nil || request.Config.TargetRevision != nil || request.Allocation.Kind != "unallocated" {
		t.Fatalf("unexpected request: %+v", request)
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(raw, got) {
		t.Fatal("configuration rewritten")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("GTL_HOME created")
	}
	for _, path := range []string{request.Target.Path, filepath.Join(root, ".git", "FETCH_HEAD")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspection effect at %s", path)
		}
	}
	old := claimInspectJSON
	claimInspectJSON = true
	t.Cleanup(func() { claimInspectJSON = old })
	before := updateNoticeVersion
	updateNoticeVersion = "fixture"
	t.Cleanup(func() { updateNoticeVersion = before })
	if readOnly, err := readOnlyIntrospection(claimCmd); err != nil || !readOnly {
		t.Fatal("root hooks not suppressed")
	}
}
func TestHostClaimFreshAndExisting(t *testing.T) {
	root, seed := hostClaimBranchFixture(t)
	request := hostClaimInspect(t, root, "feature/test")
	result := hostClaimExecute(t, root, request)
	if result.Outcome != "succeeded" || result.Effect != "retained" || result.Worktree == nil || result.Worktree.Head != runGit(t, seed, "rev-parse", "HEAD") || len(result.Warnings) != 0 {
		t.Fatalf("receipt: %+v", result)
	}
	if fixture := os.Getenv("GTL_CLAIM_FIXTURE_DIR"); fixture != "" {
		if err := os.MkdirAll(fixture, 0700); err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]any{"request": request, "succeeded": result} {
			raw, _ := json.MarshalIndent(value, "", "  ")
			if err := os.WriteFile(filepath.Join(fixture, name+".json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	next := hostClaimInspect(t, root, "feature/test")
	if next.Target.Existing == nil {
		t.Fatal("holder not captured")
	}
	runGit(t, seed, "commit", "--allow-empty", "-m", "new remote")
	runGit(t, seed, "push", "origin", "feature/test")
	advanced := hostClaimExecute(t, root, next)
	if advanced.Outcome != "succeeded" || advanced.Worktree.Head != runGit(t, seed, "rev-parse", "HEAD") {
		t.Fatalf("reclaim: %+v", advanced)
	}
}
func TestHostClaimDirtyDivergenceSucceedsWithWarning(t *testing.T) {
	root, seed := hostClaimBranchFixture(t)
	initial := hostClaimExecute(t, root, hostClaimInspect(t, root, "feature/test"))
	if initial.Worktree == nil {
		t.Fatal(initial)
	}
	target := initial.Worktree.Identity.Path
	runGit(t, target, "commit", "--allow-empty", "-m", "unique local")
	if err := os.WriteFile(filepath.Join(target, "untracked"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	head := runGit(t, target, "rev-parse", "HEAD")
	request := hostClaimInspect(t, root, "feature/test")
	runGit(t, seed, "commit", "--allow-empty", "-m", "other remote")
	runGit(t, seed, "push", "origin", "feature/test")
	result := hostClaimExecute(t, root, request)
	if result.Outcome != "succeeded" || result.Worktree.Head != head || !reflect.DeepEqual(result.Warnings, []hostClaimWarning{{"pull", "diverged"}}) {
		t.Fatalf("receipt: %+v", result)
	}
	if raw, _ := os.ReadFile(filepath.Join(target, "untracked")); string(raw) != "keep" {
		t.Fatal("untracked file changed")
	}
}
func TestHostClaimStaleIntentRefusesBeforeFetch(t *testing.T) {
	for _, kind := range []string{"source_head", "source_branch", "origin", "origin_rewrite", "config", "user_config", "new_holder", "occupied_target", "allocation", "target_head", "parent_identity"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := hostClaimBranchFixture(t)
			if kind == "target_head" {
				result := hostClaimExecute(t, root, hostClaimInspect(t, root, "feature/test"))
				if result.Outcome != "succeeded" {
					t.Fatal(result)
				}
			}
			request := hostClaimInspect(t, root, "feature/test")
			switch kind {
			case "source_head":
				runGit(t, root, "commit", "--allow-empty", "-m", "changed")
			case "source_branch":
				runGit(t, root, "checkout", "--detach")
			case "origin":
				runGit(t, root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))
			case "origin_rewrite":
				origin := runGit(t, root, "remote", "get-url", "origin")
				runGit(t, root, "config", "url."+filepath.Join(t.TempDir(), "rewritten")+".insteadOf", origin)
			case "config":
				_ = os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte("project: changed\n"), 0600)
			case "user_config":
				_ = os.WriteFile(platform.ConfigFile(), []byte("{\"port\":{\"base\":9999}}"), 0600)
			case "new_holder":
				runGit(t, root, "fetch", "origin", "feature/test")
				runGit(t, root, "worktree", "add", filepath.Join(t.TempDir(), "holder"), "feature/test")
			case "occupied_target":
				_ = os.MkdirAll(request.Target.Path, 0700)
			case "allocation":
				if err := registry.New("").Allocate(registry.Allocation{"worktree": request.Target.Path, "project": "fixture"}); err != nil {
					t.Fatal(err)
				}
			case "target_head":
				runGit(t, request.Target.Path, "commit", "--allow-empty", "-m", "changed")
			case "parent_identity":
				request.Target.Parent.Directory.Ino = "1"
			}
			fetch := filepath.Join(root, ".git", "FETCH_HEAD")
			before, _ := os.ReadFile(fetch)
			result := hostClaimExecute(t, root, request)
			after, _ := os.ReadFile(fetch)
			if result.Outcome != "failed" || result.Stage != "admission" || result.Effect != "none" || !bytes.Equal(before, after) {
				t.Fatalf("stale intent ran: %+v", result)
			}
		})
	}
}
func TestHostClaimInvalidStoresFailInspection(t *testing.T) {
	for _, kind := range []string{"project", "user", "registry"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := hostClaimBranchFixture(t)
			path := filepath.Join(root, config.ProjectConfigFile)
			if kind == "user" {
				path = platform.ConfigFile()
			}
			if kind == "registry" {
				path = platform.RegistryFile()
			}
			if err := os.WriteFile(path, []byte("[broken"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := inspectHostClaim(context.Background(), root, "feature/test", claimHostTestID); err == nil {
				t.Fatal("corrupt input accepted")
			}
		})
	}
}
func TestHostClaimStrictJSON(t *testing.T) {
	root, _ := hostClaimBranchFixture(t)
	request := hostClaimInspect(t, root, "feature/test")
	raw, _ := json.Marshal(request)
	if decoded, err := decodeHostClaim(bytes.NewReader(raw)); err != nil || !reflect.DeepEqual(decoded, request) {
		t.Fatal(err)
	}
	for _, value := range []string{strings.Replace(string(raw), "\"schema\":1", "\"schema\":1,\"extra\":0", 1), strings.Replace(string(raw), "\"schema\":1", "\"schema\":1,\"schema\":1", 1), strings.Replace(string(raw), "\"existing\":null,", "", 1), string(raw) + "{}", strings.Repeat(" ", hostClaimLimit+1)} {
		if _, err := decodeHostClaim(strings.NewReader(value)); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}
func TestHostClaimSetupReceipts(t *testing.T) {
	for _, kind := range []string{"success", "retained", "rolled_back", "unknown", "replacement"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := hostClaimBranchFixture(t)
			if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte("project: fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			request := hostClaimInspect(t, root, "feature/test")
			previous := hostClaimSetup
			t.Cleanup(func() { hostClaimSetup = previous })
			hostClaimSetup = func(_ *cobra.Command, r hostClaimRequest, _ *config.UserConfig, locks *registry.LifecycleLocks) (string, error) {
				if kind == "rolled_back" {
					runGit(t, root, "worktree", "remove", r.Target.Path)
					return "rolled_back", errors.New("injected")
				}
				if kind == "unknown" {
					runGit(t, root, "worktree", "remove", r.Target.Path)
					return "unknown", errors.New("injected")
				}
				if kind == "replacement" {
					runGit(t, r.Target.Path, "checkout", "--detach")
					return "retained", nil
				}
				if err := registry.New("").Allocate(registry.Allocation{"worktree": r.Target.Path, "project": "fixture"}); err != nil {
					t.Fatal(err)
				}
				if kind == "retained" {
					return "retained", errors.New("injected")
				}
				return "retained", nil
			}
			result := hostClaimExecute(t, root, request)
			switch kind {
			case "success":
				if result.Outcome != "succeeded" || result.Worktree.Allocation.Generation == nil {
					t.Fatal(result)
				}
			case "retained":
				if result.Outcome != "failed" || result.Effect != "retained" || result.Worktree == nil {
					t.Fatal(result)
				}
			case "rolled_back":
				if result.Outcome != "failed" || result.Effect != "rolled_back" || result.Worktree != nil {
					t.Fatal(result)
				}
			default:
				if result.Outcome != "uncertain" || result.Effect != "unknown" {
					t.Fatal(result)
				}
			}
		})
	}
}
func TestHostClaimDetachedAndUnavailableRemote(t *testing.T) {
	root, _ := claimFixture(t)
	runGit(t, root, "branch", "offline")
	runGit(t, root, "checkout", "--detach")
	runGit(t, root, "remote", "remove", "origin")
	request := hostClaimInspect(t, root, "offline")
	if request.SourceBranch != nil || request.OriginRevision != nil {
		t.Fatal(request)
	}
	result := hostClaimExecute(t, root, request)
	if result.Outcome != "succeeded" || len(result.Warnings) != 2 {
		t.Fatal(result)
	}
}
func TestHostClaimMachineOutput(t *testing.T) {
	root, _ := hostClaimBranchFixture(t)
	chdir(t, root)
	oldInspect, oldID, oldFile, oldPath, oldSetup := claimInspectJSON, claimOperationID, claimRequestFile, claimPath, claimNoSetup
	t.Cleanup(func() {
		claimInspectJSON, claimOperationID, claimRequestFile, claimPath, claimNoSetup = oldInspect, oldID, oldFile, oldPath, oldSetup
	})
	claimInspectJSON, claimOperationID, claimRequestFile, claimPath, claimNoSetup = true, claimHostTestID, "", "", false
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	if err := runHostClaim(cmd, "feature/test"); err != nil {
		t.Fatal(err)
	}
	request, err := decodeHostClaim(&out)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(request)
	file := filepath.Join(t.TempDir(), "request.json")
	_ = os.WriteFile(file, raw, 0600)
	claimInspectJSON, claimOperationID, claimRequestFile = false, "", file
	out.Reset()
	if err := runHostClaim(cmd, "feature/test"); err != nil {
		t.Fatal(err)
	}
	var result hostClaimReceipt
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Outcome != "succeeded" {
		t.Fatalf("machine result: %s %v", out.String(), err)
	}
}

func TestHostClaimExactWhitespaceHolderAndAllocation(t *testing.T) {
	root, _ := hostClaimBranchFixture(t)
	runGit(t, root, "fetch", "origin", "feature/test")
	target := filepath.Join(filepath.Dir(root), " leading\ntrailing ")
	runGit(t, root, "worktree", "add", target, "feature/test")
	if err := registry.New("").Allocate(registry.Allocation{"worktree": target, "project": "fixture"}); err != nil {
		t.Fatal(err)
	}
	request := hostClaimInspect(t, root, "feature/test")
	if request.Target.Path != target || request.Target.Existing == nil || request.Allocation.Kind != "allocated" {
		t.Fatal(request)
	}
	result := hostClaimExecute(t, root, request)
	if result.Outcome != "succeeded" || result.Worktree.Identity.Path != target || !reflect.DeepEqual(result.Worktree.Allocation, request.Allocation) {
		t.Fatal(result)
	}
}

func TestHostClaimReadEnvironmentAndBoundaries(t *testing.T) {
	root, _ := hostClaimBranchFixture(t)
	for _, branch := range []string{"--help", "@{-1}", "a..b", "a\x00b"} {
		if _, err := inspectHostClaim(context.Background(), root, branch, claimHostTestID); err == nil {
			t.Fatalf("invalid branch %q", branch)
		}
	}
	t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
	if _, err := inspectHostClaim(context.Background(), root, "feature/test", claimHostTestID); err == nil {
		t.Fatal("redirection accepted")
	}
	var limited claimBoundedBuffer
	if _, err := limited.Write(make([]byte, hostClaimLimit+1)); err == nil || limited.Len() != 0 {
		t.Fatal("output limit bypass")
	}
}

func TestHostClaimRevalidatesUnderLifecycleLock(t *testing.T) {
	root, _ := hostClaimBranchFixture(t)
	request := hostClaimInspect(t, root, "feature/test")
	acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
		if !reflect.DeepEqual(paths, []string{request.Source.Path, request.Target.Path}) {
			t.Fatalf("wrong lock targets: %v", paths)
		}
		if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte("project: changed\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return &registry.LifecycleLocks{}, nil
	}
	result := hostClaimExecute(t, root, request)
	if result.Stage != "admission" || result.Effect != "none" || result.Outcome != "failed" {
		t.Fatal(result)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "FETCH_HEAD")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fetch happened before locked revalidation")
	}
}

func TestHostClaimMissingBranchIsNotUntouched(t *testing.T) {
	root, _ := claimFixture(t)
	request := hostClaimInspect(t, root, "not-pushed")
	result := hostClaimExecute(t, root, request)
	if result.Outcome != "uncertain" || result.Effect != "unknown" || result.Failure.Code != "branch_unavailable" || result.Worktree != nil {
		t.Fatal(result)
	}
	if _, err := os.Stat(request.Target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing branch created a checkout")
	}
}

func TestHostClaimMachineModeSuppressesSetupOutput(t *testing.T) {
	root, _ := hostClaimBranchFixture(t)
	chdir(t, root)
	if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte("project: fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := hostClaimInspect(t, root, "feature/test")
	raw, _ := json.Marshal(request)
	file := filepath.Join(t.TempDir(), "request.json")
	_ = os.WriteFile(file, raw, 0600)
	oldInspect, oldID, oldFile, oldPath, oldNoSetup, oldSetup := claimInspectJSON, claimOperationID, claimRequestFile, claimPath, claimNoSetup, hostClaimSetup
	t.Cleanup(func() {
		claimInspectJSON, claimOperationID, claimRequestFile, claimPath, claimNoSetup, hostClaimSetup = oldInspect, oldID, oldFile, oldPath, oldNoSetup, oldSetup
	})
	claimInspectJSON, claimOperationID, claimRequestFile, claimPath, claimNoSetup = false, "", file, "", false
	hostClaimSetup = func(_ *cobra.Command, r hostClaimRequest, _ *config.UserConfig, _ *registry.LifecycleLocks) (string, error) {
		_, _ = os.Stdout.WriteString("sensitive setup stdout")
		_, _ = os.Stderr.WriteString("sensitive setup stderr")
		return "retained", registry.New("").Allocate(registry.Allocation{"worktree": r.Target.Path, "project": "fixture"})
	}
	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)
	command.SetContext(context.Background())
	stdout, stderr, err := captureStdIO(t, func() error { return runHostClaim(command, "feature/test") })
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("machine diagnostic leak: %q %q %v", stdout, stderr, err)
	}
	var receipt hostClaimReceipt
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.Outcome != "succeeded" {
		t.Fatalf("invalid receipt: %v", err)
	}
}
