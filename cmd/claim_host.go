package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

const hostClaimLimit = 128 * 1024

type hostClaimAllocation struct {
	Kind       string  `json:"kind"`
	Generation *string `json:"generation"`
	Revision   *string `json:"revision"`
}
type hostClaimParent struct {
	Path      string                     `json:"path"`
	Directory registry.DirectoryIdentity `json:"directory"`
}
type hostClaimExisting struct {
	Identity registry.TargetIdentity `json:"identity"`
	Head     string                  `json:"head"`
}
type hostClaimTarget struct {
	Path     string             `json:"path"`
	Existing *hostClaimExisting `json:"existing"`
	Parent   hostClaimParent    `json:"parent"`
}
type hostClaimConfig struct {
	ProjectRevision string  `json:"project_revision"`
	UserRevision    string  `json:"user_revision"`
	TargetRevision  *string `json:"target_revision"`
}
type hostClaimRequest struct {
	Schema         int                     `json:"schema"`
	OperationID    string                  `json:"operation_id"`
	Branch         string                  `json:"branch"`
	Source         registry.TargetIdentity `json:"source"`
	SourceHead     string                  `json:"source_head"`
	SourceBranch   *string                 `json:"source_branch"`
	Target         hostClaimTarget         `json:"target"`
	Config         hostClaimConfig         `json:"config"`
	OriginRevision *string                 `json:"origin_revision"`
	Allocation     hostClaimAllocation     `json:"allocation"`
}
type hostClaimWorktree struct {
	Identity   registry.TargetIdentity `json:"identity"`
	Branch     string                  `json:"branch"`
	Head       string                  `json:"head"`
	Allocation hostClaimAllocation     `json:"allocation"`
}
type hostClaimWarning struct {
	Stage string `json:"stage"`
	Code  string `json:"code"`
}
type hostClaimFailure struct {
	Code string `json:"code"`
}
type hostClaimReceipt struct {
	Schema      int                `json:"schema"`
	OperationID string             `json:"operation_id"`
	Request     hostClaimRequest   `json:"request"`
	Outcome     string             `json:"outcome"`
	Stage       string             `json:"stage"`
	Worktree    *hostClaimWorktree `json:"worktree"`
	Effect      string             `json:"effect"`
	Warnings    []hostClaimWarning `json:"warnings"`
	Failure     *hostClaimFailure  `json:"failure"`
}

func claimHostError(code string) error { return fmt.Errorf("claim_%s", code) }
func claimDigest(raw []byte) string    { hash := sha256.Sum256(raw); return hex.EncodeToString(hash[:]) }
func claimString(value string) *string { return &value }

// Git helpers below and the existing creation/setup helpers must see the same
// repository. Refuse inherited redirection instead of silently checking one
// repository and mutating another.
func claimHostEnvironment() error {
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_SHALLOW_FILE", "GIT_REPLACE_REF_BASE"} {
		if _, exists := os.LookupEnv(name); exists {
			return claimHostError("git_environment")
		}
	}
	return nil
}

type claimBoundedBuffer struct{ data bytes.Buffer }

func (b *claimBoundedBuffer) Len() int       { return b.data.Len() }
func (b *claimBoundedBuffer) String() string { return b.data.String() }

func (b *claimBoundedBuffer) Write(p []byte) (int, error) {
	if len(p) > hostClaimLimit-b.Len() {
		return 0, claimHostError("output_limit")
	}
	return b.data.Write(p)
}
func claimGit(ctx context.Context, dir string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	command.WaitDelay = time.Second
	var out, stderr claimBoundedBuffer
	command.Stdout = &out
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return "", -1, claimHostError("git_unavailable")
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", exit.ExitCode(), nil
		}
		return "", -1, claimHostError("git_unavailable")
	}
	raw := out.String()
	if !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return "", -1, claimHostError("git_unavailable")
	}
	return strings.TrimSuffix(raw, "\n"), 0, nil
}
func claimHead(ctx context.Context, path string) (string, error) {
	head, status, err := claimGit(ctx, path, "rev-parse", "--verify", "HEAD")
	if err != nil || status != 0 || (len(head) != 40 && len(head) != 64) || strings.Trim(head, "0123456789abcdef") != "" {
		return "", claimHostError("head_unavailable")
	}
	return head, nil
}
func claimBranch(ctx context.Context, path string) (*string, error) {
	branch, status, err := claimGit(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return nil, err
	}
	if status == 1 {
		return nil, nil
	}
	if status != 0 || branch == "" {
		return nil, claimHostError("branch_unavailable")
	}
	return &branch, nil
}
func claimOriginRevision(ctx context.Context, path string) (*string, error) {
	raw, status, err := claimGit(ctx, path, "config", "--get-all", "remote.origin.url")
	if err != nil || status != 0 && status != 1 {
		return nil, claimHostError("origin_unavailable")
	}
	if status == 1 {
		return nil, nil
	}
	resolved, status, err := claimGit(ctx, path, "remote", "get-url", "--all", "origin")
	if err != nil || status != 0 {
		return nil, claimHostError("origin_unavailable")
	}
	settings, status, err := claimGit(ctx, path, "config", "--get-regexp", `^remote\.origin\.`)
	if err != nil || status != 0 {
		return nil, claimHostError("origin_unavailable")
	}
	value, _ := json.Marshal([]string{raw, resolved, settings})
	return claimString(claimDigest(value)), nil
}

func claimFileRevision(path string) (string, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return claimDigest([]byte("absent")), nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > hostClaimLimit {
		return "", claimHostError("config_unavailable")
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return claimDigest([]byte("absent")), nil
	}
	if err != nil {
		return "", claimHostError("config_unavailable")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > hostClaimLimit {
		return "", claimHostError("config_unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(file, hostClaimLimit+1))
	if err != nil || len(raw) > hostClaimLimit {
		return "", claimHostError("config_unavailable")
	}
	return claimDigest(append([]byte("present\x00"), raw...)), nil
}
func claimAllocation(path string) (hostClaimAllocation, error) {
	allocation, err := registry.New("").FindForRelease(path)
	if err != nil {
		return hostClaimAllocation{}, claimHostError("registry_unavailable")
	}
	if allocation == nil {
		return hostClaimAllocation{Kind: "unallocated"}, nil
	}
	raw, err := json.Marshal(allocation)
	if err != nil || len(raw) > hostClaimLimit {
		return hostClaimAllocation{}, claimHostError("registry_unavailable")
	}
	result := hostClaimAllocation{Kind: "allocated", Revision: claimString(claimDigest(raw))}
	if generation := registry.GetString(allocation, "allocation_generation"); generation != "" {
		if !publicReleaseUUID(generation) {
			return hostClaimAllocation{}, claimHostError("registry_unavailable")
		}
		result.Generation = &generation
	}
	return result, nil
}
func claimParent(target string) (hostClaimParent, error) {
	parent := filepath.Dir(target)
	for {
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) && parent != filepath.Dir(parent) {
			parent = filepath.Dir(parent)
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return hostClaimParent{}, claimHostError("parent_unavailable")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return hostClaimParent{}, claimHostError("parent_unavailable")
		}
		return hostClaimParent{Path: parent, Directory: registry.DirectoryIdentity{Dev: fmt.Sprint(stat.Dev), Ino: fmt.Sprint(stat.Ino)}}, nil
	}
}

func inspectHostClaim(ctx context.Context, cwd, branch, operation string) (hostClaimRequest, error) {
	invalid := func() (hostClaimRequest, error) { return hostClaimRequest{}, claimHostError("inspection_refused") }
	if !publicReleaseUUID(operation) || len(branch) > 1024 || !utf8.ValidString(branch) || strings.ContainsRune(branch, 0) || strings.HasPrefix(branch, "-") {
		return invalid()
	}
	if err := claimHostEnvironment(); err != nil {
		return hostClaimRequest{}, err
	}
	checked, status, err := claimGit(ctx, cwd, "check-ref-format", "--branch", branch)
	if err != nil || status != 0 || checked != branch {
		return invalid()
	}
	main, existing, err := worktree.DiscoverWorktreeTargets(cwd, branch)
	if err != nil {
		return invalid()
	}
	main, err = registry.CanonicalLifecycleTarget(main)
	if err != nil {
		return invalid()
	}
	source, err := registry.CaptureReleaseTarget(main)
	if err != nil || source.Common == nil {
		return invalid()
	}
	pc := config.LoadProjectConfigReadOnlyQuiet(main)
	if pc.Validate() != nil {
		return hostClaimRequest{}, claimHostError("config_unavailable")
	}
	uc := config.LoadUserConfig("")
	if uc.LoadError() != nil {
		return hostClaimRequest{}, claimHostError("config_unavailable")
	}
	target := existing
	if target == "" {
		target = resolveWorktreePath("", main, pc.Project(), branch, uc)
	}
	target, err = registry.CanonicalLifecycleTarget(target)
	if err != nil {
		return invalid()
	}
	request := hostClaimRequest{Schema: 1, OperationID: operation, Branch: branch, Source: source, Target: hostClaimTarget{Path: target}}
	request.SourceHead, err = claimHead(ctx, main)
	if err != nil {
		return invalid()
	}
	request.SourceBranch, err = claimBranch(ctx, main)
	if err != nil {
		return invalid()
	}
	request.Target.Parent, err = claimParent(target)
	if err != nil {
		return invalid()
	}
	if existing != "" {
		identity, e := registry.CaptureReleaseTarget(target)
		if e != nil || !reflect.DeepEqual(identity.Common, source.Common) {
			return invalid()
		}
		current, e := claimBranch(ctx, target)
		if e != nil || current == nil || *current != branch {
			return invalid()
		}
		head, e := claimHead(ctx, target)
		if e != nil {
			return invalid()
		}
		request.Target.Existing = &hostClaimExisting{Identity: identity, Head: head}
		if config.LoadProjectConfigReadOnlyQuiet(target).Validate() != nil {
			return hostClaimRequest{}, claimHostError("config_unavailable")
		}
		revision, e := claimFileRevision(filepath.Join(target, config.ProjectConfigFile))
		if e != nil {
			return hostClaimRequest{}, e
		}
		request.Config.TargetRevision = &revision
	} else if _, e := os.Lstat(target); !errors.Is(e, os.ErrNotExist) {
		return invalid()
	}
	request.Config.ProjectRevision, err = claimFileRevision(filepath.Join(main, config.ProjectConfigFile))
	if err != nil {
		return hostClaimRequest{}, err
	}
	request.Config.UserRevision, err = claimFileRevision(uc.Path)
	if err != nil {
		return hostClaimRequest{}, err
	}
	request.OriginRevision, err = claimOriginRevision(ctx, main)
	if err != nil {
		return invalid()
	}
	request.Allocation, err = claimAllocation(target)
	if err != nil {
		return hostClaimRequest{}, err
	}
	if existing == "" && request.Allocation.Kind != "unallocated" {
		return invalid()
	}
	if err = registry.New("").CheckReleaseReservations([]string{main, target}); err != nil {
		return hostClaimRequest{}, claimHostError("reservation_refused")
	}
	if ctx.Err() != nil {
		return invalid()
	}
	return request, nil
}

func decodeHostClaim(reader io.Reader) (hostClaimRequest, error) {
	var request hostClaimRequest
	raw, err := io.ReadAll(io.LimitReader(reader, hostClaimLimit+1))
	if err != nil || len(raw) > hostClaimLimit || !utf8.Valid(raw) {
		return request, claimHostError("invalid_request")
	}
	if uniqueReleaseRequestKeys(json.NewDecoder(bytes.NewReader(raw))) != nil {
		return request, claimHostError("invalid_request")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		return request, claimHostError("invalid_request")
	}
	canonical, _ := json.Marshal(request)
	var original, normalized any
	if json.Unmarshal(raw, &original) != nil || json.Unmarshal(canonical, &normalized) != nil || !reflect.DeepEqual(original, normalized) || request.Schema != 1 || !publicReleaseUUID(request.OperationID) {
		return request, claimHostError("invalid_request")
	}
	return request, nil
}

// Tests replace setup effects while keeping real Git identity and admission checks.
var hostClaimSetup = func(cmd *cobra.Command, request hostClaimRequest, uc *config.UserConfig, locks *registry.LifecycleLocks) (string, error) {
	if request.Target.Existing == nil {
		_, effect, err := runSetupWithRollbackOutcome(cmd, request.Target.Path, request.Source.Path, uc, io.Discard, false, locks)
		return effect, err
	}
	allocation, err := registry.New("").FindForRelease(request.Target.Path)
	if err != nil {
		return "unknown", claimHostError("registry_unavailable")
	}
	if allocation != nil {
		return "retained", nil
	}
	s := setup.New(request.Target.Path, request.Source.Path, uc)
	s.Log = io.Discard
	s.Options.StrictDB = false
	_, err = s.Run()
	return "retained", err
}

func observeHostClaim(ctx context.Context, request hostClaimRequest) (*hostClaimWorktree, error) {
	identity, err := registry.CaptureReleaseTarget(request.Target.Path)
	if err != nil || !reflect.DeepEqual(identity.Common, request.Source.Common) {
		return nil, claimHostError("identity_changed")
	}
	if request.Target.Existing != nil && !reflect.DeepEqual(identity, request.Target.Existing.Identity) {
		return nil, claimHostError("identity_changed")
	}
	branch, err := claimBranch(ctx, identity.Path)
	if err != nil || branch == nil || *branch != request.Branch {
		return nil, claimHostError("branch_changed")
	}
	head, err := claimHead(ctx, identity.Path)
	if err != nil {
		return nil, err
	}
	allocation, err := claimAllocation(identity.Path)
	if err != nil {
		return nil, err
	}
	return &hostClaimWorktree{Identity: identity, Branch: *branch, Head: head, Allocation: allocation}, nil
}

func claimSourceUnchanged(ctx context.Context, request hostClaimRequest, checkConfig bool) bool {
	identity, err := registry.CaptureReleaseTarget(request.Source.Path)
	if err != nil || !reflect.DeepEqual(identity, request.Source) {
		return false
	}
	head, err := claimHead(ctx, request.Source.Path)
	if err != nil || head != request.SourceHead {
		return false
	}
	branch, err := claimBranch(ctx, request.Source.Path)
	if err != nil || !reflect.DeepEqual(branch, request.SourceBranch) {
		return false
	}
	revision, err := claimOriginRevision(ctx, request.Source.Path)
	if err != nil || !reflect.DeepEqual(revision, request.OriginRevision) {
		return false
	}
	if !checkConfig {
		return true
	}
	project, err := claimFileRevision(filepath.Join(request.Source.Path, config.ProjectConfigFile))
	if err != nil || project != request.Config.ProjectRevision {
		return false
	}
	user := config.LoadUserConfig("")
	revisionValue, err := claimFileRevision(user.Path)
	if err != nil || user.LoadError() != nil || revisionValue != request.Config.UserRevision {
		return false
	}
	if request.Config.TargetRevision != nil {
		target, err := claimFileRevision(filepath.Join(request.Target.Path, config.ProjectConfigFile))
		if err != nil || target != *request.Config.TargetRevision {
			return false
		}
	}
	return true
}

func executeHostClaim(cmd *cobra.Command, cwd string, request hostClaimRequest) hostClaimReceipt {
	result := hostClaimReceipt{Schema: 1, OperationID: request.OperationID, Request: request, Outcome: "failed", Stage: "admission", Effect: "none", Warnings: []hostClaimWarning{}}
	fail := func(code string) hostClaimReceipt { result.Failure = &hostClaimFailure{Code: code}; return result }
	selection := func() (creationSelection, error) {
		current, err := inspectHostClaim(cmd.Context(), cwd, request.Branch, request.OperationID)
		if err != nil {
			return creationSelection{}, err
		}
		if !reflect.DeepEqual(current, request) {
			return creationSelection{}, claimHostError("binding_conflict")
		}
		existing := ""
		if request.Target.Existing != nil {
			existing = request.Target.Path
		}
		return creationSelection{source: request.Source.Path, target: request.Target.Path, existing: existing}, nil
	}
	admission, err := admitCreation(cmd.Context(), selection)
	if err != nil {
		return fail("admission_refused")
	}
	defer admission.locks.Close()
	if err = admission.recheck(); err != nil {
		return fail("binding_conflict")
	}
	result.Stage = "fetch"
	result.Effect = "unknown"
	if err = worktree.FetchInRepo(request.Source.Path, "origin", request.Branch); err != nil {
		result.Warnings = append(result.Warnings, hostClaimWarning{"fetch", "fetch_failed"})
	}
	// Fetch may change refs, but may not change the selected holder or captured
	// source/target checkout, config, allocation, or remote binding.
	current, err := inspectHostClaim(cmd.Context(), cwd, request.Branch, request.OperationID)
	if err != nil || !reflect.DeepEqual(current, request) {
		result.Outcome = "uncertain"
		return fail("binding_changed_after_fetch")
	}
	if request.Target.Existing == nil {
		result.Stage = "checkout"
		if !worktree.BranchExistsInRepo(request.Source.Path, request.Branch) {
			result.Outcome = "uncertain"
			return fail("branch_unavailable")
		}
		if err = ensureGitignored(request.Source.Path, request.Target.Path, io.Discard); err != nil {
			result.Outcome = "uncertain"
			return fail("checkout_failed")
		}
		if err = worktree.CreateWithOptions(request.Target.Path, request.Branch, false, "", worktree.CreateOptions{DeferSetup: true}); err != nil {
			result.Outcome = "uncertain"
			return fail("checkout_failed")
		}
	}
	if !claimSourceUnchanged(cmd.Context(), request, true) {
		result.Outcome = "uncertain"
		return fail("binding_changed_after_checkout")
	}
	checked, err := observeHostClaim(cmd.Context(), request)
	if err != nil {
		result.Outcome = "uncertain"
		return fail("identity_changed")
	}
	if !reflect.DeepEqual(checked.Allocation, request.Allocation) || request.Target.Existing != nil && checked.Head != request.Target.Existing.Head {
		result.Outcome = "uncertain"
		return fail("binding_changed_after_checkout")
	}
	result.Worktree = checked
	result.Effect = "retained"
	pc := config.LoadProjectConfigReadOnlyQuiet(request.Source.Path)
	if pc.Validate() != nil {
		result.Outcome = "uncertain"
		return fail("config_unavailable")
	}
	if pc.Exists() {
		result.Stage = "setup"
		if config.LoadProjectConfigReadOnlyQuiet(request.Target.Path).Validate() != nil {
			return fail("config_unavailable")
		}
		effect, setupErr := hostClaimSetup(cmd, request, config.LoadUserConfig(""), admission.locks)
		if setupErr != nil {
			result.Effect = effect
			result.Worktree = nil
			if effect == "retained" {
				result.Worktree, err = observeHostClaim(cmd.Context(), request)
				if err != nil {
					result.Effect = "unknown"
				}
			}
			if effect == "rolled_back" {
				allocation, e := claimAllocation(request.Target.Path)
				if e != nil || allocation.Kind != "unallocated" {
					result.Effect = "unknown"
				}
			}
			if result.Effect == "unknown" {
				result.Outcome = "uncertain"
			}
			return fail("setup_failed")
		}
	}
	// Setup can run configured hooks; never pull a replacement checkout.
	after, err := observeHostClaim(cmd.Context(), request)
	if err != nil || !reflect.DeepEqual(after.Identity, checked.Identity) || after.Head != checked.Head || !claimSourceUnchanged(cmd.Context(), request, false) {
		result.Outcome = "uncertain"
		result.Effect = "unknown"
		result.Worktree = nil
		return fail("identity_changed_after_setup")
	}
	result.Stage = "pull"
	if err = worktree.Pull(request.Target.Path, "origin", request.Branch); err != nil {
		code := "pull_failed"
		if worktree.IsDivergedPull(err) {
			code = "diverged"
		}
		result.Warnings = append(result.Warnings, hostClaimWarning{"pull", code})
	}
	final, err := observeHostClaim(cmd.Context(), request)
	if err != nil || !reflect.DeepEqual(final.Identity, checked.Identity) || !reflect.DeepEqual(final.Allocation, after.Allocation) {
		result.Outcome = "uncertain"
		result.Effect = "unknown"
		result.Worktree = nil
		return fail("postcondition_unavailable")
	}
	if pc.Exists() && final.Allocation.Kind != "allocated" {
		result.Outcome = "uncertain"
		result.Worktree = final
		return fail("allocation_unavailable")
	}
	result.Worktree = final
	result.Outcome = "succeeded"
	result.Stage = "complete"
	return result
}

func runHostClaim(cmd *cobra.Command, branch string) error {
	cmd.SilenceUsage = true
	outputWriter := cmd.OutOrStdout()
	// Legacy setup hooks bind process stdout/stderr directly. Keep their output
	// outside the machine receipt and do not disclose config-derived diagnostics.
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return claimHostError("output_unavailable")
	}
	previousOut, previousErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = null, null
	defer func() { os.Stdout, os.Stderr = previousOut, previousErr; _ = null.Close() }()
	if claimPath != "" || claimNoSetup || claimInspectJSON == (claimRequestFile != "") || claimInspectJSON && !publicReleaseUUID(claimOperationID) || claimRequestFile != "" && claimOperationID != "" {
		return claimHostError("invalid_request")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return claimHostError("cwd_unavailable")
	}
	var output any
	if claimInspectJSON {
		request, e := inspectHostClaim(cmd.Context(), cwd, branch, claimOperationID)
		if e != nil {
			return e
		}
		checked, e := inspectHostClaim(cmd.Context(), cwd, branch, claimOperationID)
		if e != nil || !reflect.DeepEqual(request, checked) {
			return claimHostError("binding_conflict")
		}
		output = request
	} else {
		named, e := os.Lstat(claimRequestFile)
		if e != nil || !named.Mode().IsRegular() || named.Size() > hostClaimLimit {
			return claimHostError("invalid_request")
		}
		fd, e := syscall.Open(claimRequestFile, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return claimHostError("invalid_request")
		}
		file := os.NewFile(uintptr(fd), claimRequestFile)
		defer file.Close()
		info, e := file.Stat()
		if e != nil || !os.SameFile(named, info) || !info.Mode().IsRegular() || info.Size() > hostClaimLimit {
			return claimHostError("invalid_request")
		}
		request, e := decodeHostClaim(file)
		if e != nil || request.Branch != branch {
			return claimHostError("invalid_request")
		}
		output = executeHostClaim(cmd, cwd, request)
	}
	raw, err := json.Marshal(output)
	if err != nil || len(raw)+1 > hostClaimLimit {
		return claimHostError("output_limit")
	}
	if _, err = fmt.Fprintln(outputWriter, string(raw)); err != nil {
		return claimHostError("output_unavailable")
	}
	if receipt, ok := output.(hostClaimReceipt); ok && receipt.Outcome != "succeeded" {
		return claimHostError(receipt.Failure.Code)
	}
	return nil
}
