package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/git-treeline/cli/internal/worktree"
	"github.com/google/uuid"
)

const maxReleaseOperations = 1024
const maxReleaseStoreBytes = 8 * 1024 * 1024

type DirectoryIdentity struct {
	Dev string `json:"dev"`
	Ino string `json:"ino"`
}
type CommonIdentity struct {
	Path string `json:"path"`
	DirectoryIdentity
}
type TargetIdentity struct {
	Path      string            `json:"path"`
	Directory DirectoryIdentity `json:"directory"`
	Common    *CommonIdentity   `json:"common"`
}
type AllocationRef struct {
	Kind       string `json:"kind"`
	Generation string `json:"generation,omitempty"`
}
type ReleasePolicy struct {
	RemoveWorktree    bool `json:"remove_worktree"`
	DropDatabases     bool `json:"drop_databases"`
	ForceDirty        bool `json:"force_dirty"`
	IgnoreHookFailure bool `json:"ignore_hook_failure"`
}
type ReleaseRequest struct {
	OperationID            string          `json:"operation_id"`
	Target                 TargetIdentity  `json:"target"`
	Allocation             AllocationRef   `json:"allocation"`
	Policy                 ReleasePolicy   `json:"policy"`
	HooksDigest            string          `json:"hooks_digest"`
	DatabaseConfigRevision string          `json:"database_config_revision,omitempty"`
	RuntimeBinding         *RuntimeBinding `json:"runtime_binding,omitempty"`
}
type CleanupSnapshot struct {
	MainWorktree    bool     `json:"main_worktree"`
	Project         string   `json:"project"`
	Branch          string   `json:"branch"`
	WorktreeName    string   `json:"worktree_name"`
	Ports           []int    `json:"ports"`
	DatabaseAdapter string   `json:"database_adapter"`
	Databases       []string `json:"databases"`
	RedisDB         *int     `json:"redis_db,omitempty"`
}
type ReleasePhase string

const (
	PhasePreHook    ReleasePhase = "pre_release_hook"
	PhaseSupervisor ReleasePhase = "supervisor_stop"
	PhaseDatabases  ReleasePhase = "databases"
	PhaseWorktree   ReleasePhase = "worktree_remove"
	PhaseAllocation ReleasePhase = "allocation_retire"
	PhaseHosts      ReleasePhase = "managed_hosts"
	PhasePostHook   ReleasePhase = "post_release_hook"
)

type PhaseRecord struct {
	Name    ReleasePhase `json:"name"`
	State   string       `json:"state"`
	Attempt string       `json:"attempt,omitempty"`
	Code    string       `json:"code,omitempty"`
}
type ReleaseOperation struct {
	ReleaseRequest
	Snapshot  *CleanupSnapshot `json:"snapshot"`
	CreatedAt string           `json:"created_at"`
	Revision  uint64           `json:"revision"`
	Phases    []PhaseRecord    `json:"phases"`
}
type PhaseToken struct {
	OperationID string
	Phase       ReleasePhase
	Attempt     string
}
type PhaseResult struct {
	State string
	Code  string
}

func canonicalUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && len(s) == 36 && id.String() == s
}
func boundedText(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func validDirectory(d DirectoryIdentity) bool {
	for _, s := range []string{d.Dev, d.Ino} {
		n, e := strconv.ParseUint(s, 10, 64)
		if e != nil || strconv.FormatUint(n, 10) != s {
			return false
		}
	}
	return true
}
func validTarget(t TargetIdentity) bool {
	return filepath.IsAbs(t.Path) && filepath.Clean(t.Path) == t.Path && boundedText(t.Path, 4096) && validDirectory(t.Directory) && (t.Common == nil || filepath.IsAbs(t.Common.Path) && filepath.Clean(t.Common.Path) == t.Common.Path && boundedText(t.Common.Path, 4096) && validDirectory(t.Common.DirectoryIdentity))
}
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func requiredPhases(r ReleaseRequest) []ReleasePhase {
	if r.Allocation.Kind == "unallocated" {
		return []ReleasePhase{PhaseWorktree}
	}
	p := []ReleasePhase{PhasePreHook, PhaseSupervisor}
	if r.Policy.DropDatabases {
		p = append(p, PhaseDatabases)
	}
	p = append(p, PhaseAllocation, PhaseHosts, PhasePostHook)
	if r.Policy.RemoveWorktree {
		p = append(p, PhaseWorktree)
	}
	return p
}
func validLegacyRequest(r ReleaseRequest) bool {
	return canonicalUUID(r.OperationID) && validTarget(r.Target) && validDigest(r.HooksDigest) && (!r.Policy.ForceDirty || r.Policy.RemoveWorktree) && (r.Allocation.Kind == "allocated" && canonicalUUID(r.Allocation.Generation) || r.Allocation.Kind == "unallocated" && r.Allocation.Generation == "" && r.Policy.RemoveWorktree && !r.Policy.DropDatabases && !r.Policy.IgnoreHookFailure) && (!r.Policy.RemoveWorktree || r.Target.Common != nil)
}
func validRequest(r ReleaseRequest) bool {
	return validLegacyRequest(r) && (r.Policy.DropDatabases && validDigest(r.DatabaseConfigRevision) || !r.Policy.DropDatabases && r.DatabaseConfigRevision == "") && (r.Allocation.Kind == "unallocated" && r.RuntimeBinding == nil || r.Allocation.Kind == "allocated" && r.RuntimeBinding != nil && validRuntimeBinding(*r.RuntimeBinding) && r.RuntimeBinding.Kind != "unknown")
}
func validStoredRequest(r ReleaseRequest) bool {
	return validRequest(r) || r.RuntimeBinding == nil && r.DatabaseConfigRevision == "" && validLegacyRequest(r)
}
func (o ReleaseOperation) Outcome() string {
	all := true
	for _, p := range o.Phases {
		if p.State == "uncertain" || p.State == "started" {
			return "uncertain"
		}
		if p.State == "blocked" {
			return "blocked"
		}
		all = all && p.State == "completed"
	}
	if all {
		return "completed"
	}
	return "prepared"
}
func phaseComplete(o ReleaseOperation, phase ReleasePhase) bool {
	for _, p := range o.Phases {
		if p.Name == phase {
			return p.State == "completed"
		}
	}
	return false
}
func validResult(r PhaseResult) bool {
	return r.State == "completed" && (r.Code == "ok" || r.Code == "ignored_hook_failure") || r.State == "blocked" && (r.Code == "failed" || r.Code == "privilege_required" || r.Code == "config_changed" || r.Code == "identity_changed") || r.State == "uncertain" && r.Code == "unknown"
}

func validateReleaseStore(data RegistryData) error {
	targets := map[string]bool{}
	for _, a := range data.Allocations {
		p := GetString(a, "worktree")
		if !filepath.IsAbs(p) || !boundedText(p, 4096) || targets[filepath.Clean(p)] {
			return fmt.Errorf("invalid or duplicate allocation target")
		}
		targets[filepath.Clean(p)] = true
	}
	if data.Version < 3 {
		if len(data.ReleaseOperations) > 0 {
			return fmt.Errorf("release operations require schema 3")
		}
		for _, a := range data.Allocations {
			if _, ok := a["runtime_binding"]; ok {
				return fmt.Errorf("runtime bindings require schema 4")
			}
			if _, ok := a["allocation_generation"]; ok {
				return fmt.Errorf("enrollment identities require schema 3")
			}
		}
		return nil
	}
	if (data.Version != 3 && data.Version != 4) || len(data.ReleaseOperations) > maxReleaseOperations {
		return fmt.Errorf("unsupported release store or capacity")
	}
	generations := map[string]bool{}
	for _, a := range data.Allocations {
		g := GetString(a, "allocation_generation")
		if !canonicalUUID(g) || generations[g] {
			return fmt.Errorf("invalid enrollment generation")
		}
		generations[g] = true
		if data.Version == 4 {
			if _, present := a["runtime_binding"]; !present {
				return fmt.Errorf("missing runtime binding")
			}
			if _, err := AllocationRuntimeBinding(a); err != nil {
				return err
			}
		} else if _, present := a["runtime_binding"]; present {
			return fmt.Errorf("runtime bindings require schema 4")
		}
	}
	ids := map[string]bool{}
	reservations := map[string]bool{}
	reservedDirectories := map[DirectoryIdentity]bool{}
	for _, o := range data.ReleaseOperations {
		if !validStoredRequest(o.ReleaseRequest) || data.Version == 3 && (o.RuntimeBinding != nil || o.DatabaseConfigRevision != "") || ids[o.OperationID] || o.Revision == 0 {
			return fmt.Errorf("invalid release operation")
		}
		ids[o.OperationID] = true
		created, e := time.Parse(time.RFC3339Nano, o.CreatedAt)
		if e != nil || created.UTC().Format(time.RFC3339Nano) != o.CreatedAt {
			return fmt.Errorf("invalid operation timestamp")
		}
		if o.Allocation.Kind == "allocated" {
			if o.Snapshot == nil {
				return fmt.Errorf("missing cleanup snapshot")
			}
			raw, _ := json.Marshal(o.Snapshot)
			if len(raw) > 65536 {
				return fmt.Errorf("cleanup snapshot capacity")
			}
			for _, s := range []string{o.Snapshot.Project, o.Snapshot.Branch, o.Snapshot.WorktreeName, o.Snapshot.DatabaseAdapter} {
				if !boundedText(s, 4096) {
					return fmt.Errorf("invalid cleanup snapshot")
				}
			}
			if len(o.Snapshot.Ports) > 256 || len(o.Snapshot.Databases) > 256 {
				return fmt.Errorf("cleanup snapshot capacity")
			}
			for _, p := range o.Snapshot.Ports {
				if p < 1 || p > 65535 {
					return fmt.Errorf("invalid snapshot port")
				}
			}
			for _, s := range o.Snapshot.Databases {
				if !boundedText(s, 4096) || s == "" {
					return fmt.Errorf("invalid snapshot database")
				}
			}
			if o.Snapshot.RedisDB != nil && *o.Snapshot.RedisDB < 0 {
				return fmt.Errorf("invalid Redis index")
			}
		} else if o.Snapshot != nil {
			return fmt.Errorf("unexpected cleanup snapshot")
		}
		phases := requiredPhases(o.ReleaseRequest)
		if len(phases) != len(o.Phases) {
			return fmt.Errorf("invalid phases")
		}
		pending := false
		for i, p := range o.Phases {
			if p.Name != phases[i] {
				return fmt.Errorf("invalid phase order")
			}
			if p.State == "pending" {
				if p.Attempt != "" || p.Code != "" {
					return fmt.Errorf("invalid pending phase")
				}
			} else {
				if pending || !canonicalUUID(p.Attempt) {
					return fmt.Errorf("invalid phase transition")
				}
				if p.State == "started" {
					if p.Code != "" {
						return fmt.Errorf("invalid started phase")
					}
				} else if !validPhaseResult(o, p.Name, PhaseResult{p.State, p.Code}) {
					return fmt.Errorf("invalid phase receipt")
				}
			}
			pending = pending || p.State != "completed"
		}
		if phaseComplete(o, PhaseAllocation) && allocationGeneration(data, o.Allocation.Generation) != nil {
			return fmt.Errorf("retired generation reappeared")
		}
		if o.Outcome() != "completed" {
			if reservations[o.Target.Path] || reservedDirectories[o.Target.Directory] {
				return fmt.Errorf("duplicate release reservation")
			}
			reservations[o.Target.Path] = true
			reservedDirectories[o.Target.Directory] = true
			if o.Allocation.Kind == "unallocated" && allocationPath(data, o.Target.Path) != nil {
				return fmt.Errorf("unallocated reservation replaced")
			}
			if o.Allocation.Kind == "allocated" {
				a := allocationGeneration(data, o.Allocation.Generation)
				if phaseComplete(o, PhaseAllocation) {
					if a != nil || allocationPath(data, o.Target.Path) != nil {
						return fmt.Errorf("retired allocation reappeared")
					}
				} else if a == nil || GetString(a, "allocation_generation") != o.Allocation.Generation || resolvePath(GetString(a, "worktree")) != o.Target.Path {
					return fmt.Errorf("reserved enrollment changed")
				} else {
					if o.RuntimeBinding != nil {
						binding, err := AllocationRuntimeBinding(a)
						if err != nil || binding != *o.RuntimeBinding {
							return fmt.Errorf("reserved runtime binding changed")
						}
					}
					expected, err := snapshot(a)
					if err != nil || !reflect.DeepEqual(expected, o.Snapshot) {
						return fmt.Errorf("reserved cleanup snapshot changed")
					}
				}

			}
		}
	}
	return nil
}
func cloneReleaseData(d RegistryData) RegistryData {
	raw, _ := json.Marshal(d)
	var copy RegistryData
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func cloneOperation(o ReleaseOperation) ReleaseOperation {
	raw, _ := json.Marshal(o)
	var copy ReleaseOperation
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func sameJSON(a, b any) bool {
	x, e := json.Marshal(a)
	y, f := json.Marshal(b)
	return e == nil && f == nil && bytes.Equal(x, y)
}
func allocationAt(d RegistryData, p string) Allocation {
	for _, a := range d.Allocations {
		if resolvePath(GetString(a, "worktree")) == p {
			return a
		}
	}
	return nil
}
func reservationFor(d *RegistryData, p string) error {
	observed, observedErr := directoryIdentity(p)
	for _, o := range d.ReleaseOperations {
		if o.Outcome() != "completed" && (o.Target.Path == p || observedErr == nil && o.Target.Directory == observed) {
			return fmt.Errorf("target reserved by release %s", o.OperationID)
		}
	}
	return nil
}
func protectReservations(before, after RegistryData) error {
	for _, o := range before.ReleaseOperations {
		if o.Outcome() == "completed" {
			continue
		}
		if !sameJSON(allocationPath(before, o.Target.Path), allocationPath(after, o.Target.Path)) || o.Allocation.Kind == "allocated" && !sameJSON(allocationGeneration(before, o.Allocation.Generation), allocationGeneration(after, o.Allocation.Generation)) {
			return fmt.Errorf("target reserved by release %s", o.OperationID)
		}
	}
	return nil
}

func preserveGenerations(before RegistryData, after *RegistryData) {
	for _, a := range after.Allocations {
		old := allocationAt(before, resolvePath(GetString(a, "worktree")))
		if old != nil {
			if binding, ok := old["runtime_binding"]; ok {
				a["runtime_binding"] = binding
			} else {
				delete(a, "runtime_binding")
			}
			if g := GetString(old, "allocation_generation"); g != "" {
				a["allocation_generation"] = g
			} else {
				delete(a, "allocation_generation")
			}
		} else {
			delete(a, "runtime_binding")
			delete(a, "allocation_generation")
		}
	}
}
func upgradeEnrollments(d *RegistryData) {
	for _, a := range d.Allocations {
		if GetString(a, "allocation_generation") == "" {
			a["allocation_generation"] = uuid.NewString()
		}
	}
	if d.Version < 3 {
		d.Version = 3
	}
	if d.Version == 4 {
		for _, a := range d.Allocations {
			if _, present := a["runtime_binding"]; !present {
				a["runtime_binding"] = RuntimeBinding{Kind: "unknown"}
			}
		}
	}
}

func directoryIdentity(p string) (DirectoryIdentity, error) {
	s, e := os.Stat(p)
	if e != nil {
		return DirectoryIdentity{}, e
	}
	if !s.IsDir() {
		return DirectoryIdentity{}, fmt.Errorf("not a directory")
	}
	stat, ok := s.Sys().(*syscall.Stat_t)
	if !ok {
		return DirectoryIdentity{}, fmt.Errorf("directory identity unavailable")
	}
	return DirectoryIdentity{strconv.FormatUint(uint64(stat.Dev), 10), strconv.FormatUint(uint64(stat.Ino), 10)}, nil
}
func identityGit(root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor=false", "-C", root}, args...)...)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") && !strings.HasPrefix(e, "LC_ALL=") {
			c.Env = append(c.Env, e)
		}
	}
	c.Env = append(c.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	c.WaitDelay = time.Second
	return c.Output()
}

// VerifyReleaseTarget revalidates an existing target without changing registry
// state. Effect adapters use it between commands under their execution lock.
func VerifyReleaseTarget(t TargetIdentity) error {
	return verifyTarget(t, false)
}

func verifyTarget(t TargetIdentity, removed bool) error {
	if !validTarget(t) {
		return fmt.Errorf("invalid target identity")
	}
	canonical, e := filepath.EvalSymlinks(t.Path)
	if os.IsNotExist(e) && removed {
		if t.Common != nil {
			c, err := filepath.EvalSymlinks(t.Common.Path)
			if err != nil || c != t.Common.Path {
				return fmt.Errorf("common identity changed")
			}
			id, err := directoryIdentity(c)
			if err != nil || id != t.Common.DirectoryIdentity {
				return fmt.Errorf("common identity changed")
			}
		}
		return nil
	}
	if e != nil || canonical != t.Path {
		return fmt.Errorf("target identity changed")
	}
	id, e := directoryIdentity(canonical)
	if e != nil || id != t.Directory {
		return fmt.Errorf("target identity changed")
	}
	if removed {
		return fmt.Errorf("removed target reappeared")
	}
	out, e := identityGit(t.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if t.Common == nil {
		if e == nil {
			return fmt.Errorf("unexpected Git target")
		}
		exit, ok := e.(*exec.ExitError)
		if !ok || exit.ExitCode() != 128 || string(exit.Stderr) != "fatal: not a git repository (or any of the parent directories): .git\n" {
			return fmt.Errorf("Git identity unavailable")
		}
		for p := t.Path; ; p = filepath.Dir(p) {
			if _, err := os.Lstat(filepath.Join(p, ".git")); !os.IsNotExist(err) {
				return fmt.Errorf("broken Git marker")
			}
			if filepath.Dir(p) == p {
				break
			}
		}
		return nil
	}
	if e != nil || !strings.HasSuffix(string(out), "\n") {
		return fmt.Errorf("Git identity unavailable")
	}
	common, e := filepath.EvalSymlinks(strings.TrimSuffix(string(out), "\n"))
	if e != nil || common != t.Common.Path {
		return fmt.Errorf("common identity changed")
	}
	ci, e := directoryIdentity(common)
	if e != nil || ci != t.Common.DirectoryIdentity {
		return fmt.Errorf("common identity changed")
	}
	top, e := identityGit(t.Path, "rev-parse", "--show-toplevel")
	if e != nil {
		return e
	}
	root, e := filepath.EvalSymlinks(strings.TrimSuffix(string(top), "\n"))
	if e != nil || root != t.Path {
		return fmt.Errorf("not a Git worktree root")
	}
	return nil
}
func snapshot(a Allocation) (*CleanupSnapshot, error) {
	for _, key := range []string{"project", "branch", "worktree_name", "database_adapter", "database"} {
		if v, ok := a[key]; ok {
			s, ok := v.(string)
			if !ok || !boundedText(s, 4096) {
				return nil, fmt.Errorf("invalid cleanup field %s", key)
			}
		}
	}
	if v, ok := a["main_worktree"]; ok {
		if _, ok := v.(bool); !ok {
			return nil, fmt.Errorf("invalid main_worktree flag")
		}
	}
	integer := func(v any, min, max float64) bool {
		n, ok := v.(float64)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= min && n <= max && math.Trunc(n) == n
	}
	if v, ok := a["port"]; ok && !integer(v, 1, 65535) {
		return nil, fmt.Errorf("invalid cleanup port")
	}
	if v, ok := a["redis_db"]; ok && v != nil && !integer(v, 0, 2147483647) {
		return nil, fmt.Errorf("invalid cleanup Redis index")
	}
	if v, ok := a["ports"]; ok {
		values, ok := v.([]any)
		if !ok || len(values) > 256 {
			return nil, fmt.Errorf("invalid cleanup ports")
		}
		for _, n := range values {
			if !integer(n, 1, 65535) {
				return nil, fmt.Errorf("invalid cleanup ports")
			}
		}
	}
	if v, ok := a["databases"]; ok {
		values, ok := v.([]any)
		if !ok || len(values) > 256 {
			return nil, fmt.Errorf("invalid cleanup databases")
		}
		for _, v := range values {
			s, ok := v.(string)
			if !ok || s == "" || !boundedText(s, 4096) {
				return nil, fmt.Errorf("invalid cleanup databases")
			}
		}
	}
	s := &CleanupSnapshot{MainWorktree: a["main_worktree"] == true, Project: GetString(a, "project"), Branch: GetString(a, "branch"), WorktreeName: GetString(a, "worktree_name"), Ports: ExtractPorts(a), DatabaseAdapter: GetString(a, "database_adapter"), Databases: ExtractDatabases(a)}
	if n, ok := a["redis_db"].(float64); ok {
		v := int(n)
		s.RedisDB = &v
	}
	return s, nil
}

func findOperation(d *RegistryData, id string) (*ReleaseOperation, error) {
	if !canonicalUUID(id) {
		return nil, fmt.Errorf("invalid operation UUID")
	}
	for i := range d.ReleaseOperations {
		if d.ReleaseOperations[i].OperationID == id {
			return &d.ReleaseOperations[i], nil
		}
	}
	return nil, fmt.Errorf("release operation not found")
}

// EnrollAllocation is explicit development enrollment. It upgrades the store
// without adopting legacy runtime ownership: absent bindings become unknown.
func (r *Registry) EnrollAllocation(target TargetIdentity, expected Allocation) (Allocation, error) {
	var result Allocation
	err := r.registryTransaction(func(d *RegistryData) error {
		if err := verifyTarget(target, false); err != nil {
			return err
		}
		if err := reservationFor(d, target.Path); err != nil {
			return err
		}
		a := allocationAt(*d, target.Path)
		if a == nil || !sameJSON(a, expected) {
			return fmt.Errorf("enrollment snapshot changed")
		}
		d.Version = 4
		upgradeEnrollments(d)
		result = allocationAt(*d, target.Path)
		return nil
	}, true)
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (r *Registry) PrepareRelease(input ReleaseRequest) (ReleaseOperation, bool, error) {
	var result ReleaseOperation
	existing := false
	err := r.registryTransaction(func(d *RegistryData) error {
		if d.Version != 4 || !validRequest(input) {
			return fmt.Errorf("invalid release request")
		}
		for _, o := range d.ReleaseOperations {
			if o.OperationID == input.OperationID {
				if !reflect.DeepEqual(o.ReleaseRequest, input) {
					return fmt.Errorf("operation binding conflict")
				}
				result = cloneOperation(o)
				existing = true
				return nil
			}
		}
		if len(d.ReleaseOperations) >= maxReleaseOperations {
			return fmt.Errorf("release capacity exceeded")
		}
		if err := verifyTarget(input.Target, false); err != nil {
			return err
		}
		if input.Policy.RemoveWorktree {
			if _, err := worktree.ValidateRemoval(input.Target.Path); err != nil {
				return err
			}
		}
		if err := reservationFor(d, input.Target.Path); err != nil {
			return err
		}
		a := allocationAt(*d, input.Target.Path)
		if input.Allocation.Kind == "allocated" {
			if a == nil || GetString(a, "allocation_generation") != input.Allocation.Generation {
				return fmt.Errorf("allocation generation changed")
			}
			binding, err := AllocationRuntimeBinding(a)
			if err != nil || input.RuntimeBinding == nil || binding != *input.RuntimeBinding {
				return fmt.Errorf("runtime binding changed")
			}
		} else if a != nil {
			return fmt.Errorf("target is allocated")
		}
		o := ReleaseOperation{ReleaseRequest: input, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Revision: 1}
		if a != nil {
			var err error
			o.Snapshot, err = snapshot(a)
			if err != nil {
				return err
			}
		}
		for _, phase := range requiredPhases(input) {
			o.Phases = append(o.Phases, PhaseRecord{Name: phase, State: "pending"})
		}
		d.ReleaseOperations = append(d.ReleaseOperations, o)
		result = cloneOperation(o)
		return nil
	}, true)
	if err != nil {
		return ReleaseOperation{}, false, err
	}
	return result, existing, nil
}
func (r *Registry) GetRelease(id string) (*ReleaseOperation, error) {
	if !canonicalUUID(id) {
		return nil, fmt.Errorf("invalid operation UUID")
	}
	d, e := r.readReleaseStore()
	if e != nil {
		return nil, e
	}
	for _, o := range d.ReleaseOperations {
		if o.OperationID == id {
			copy := cloneOperation(o)
			return &copy, nil
		}
	}
	return nil, nil
}
func (r *Registry) ListReleases() ([]ReleaseOperation, error) {
	d, e := r.readReleaseStore()
	if e != nil {
		return nil, e
	}
	return d.ReleaseOperations, nil
}
func verifyOperation(d RegistryData, o ReleaseOperation) error {
	if !validRequest(o.ReleaseRequest) {
		return fmt.Errorf("legacy release requires retained completion bindings")
	}
	if err := verifyTarget(o.Target, phaseComplete(o, PhaseWorktree)); err != nil {
		return err
	}
	a := allocationAt(d, o.Target.Path)
	if o.Allocation.Kind == "unallocated" || phaseComplete(o, PhaseAllocation) {
		if a != nil {
			return fmt.Errorf("allocation reappeared")
		}
	} else if a == nil || GetString(a, "allocation_generation") != o.Allocation.Generation {
		return fmt.Errorf("allocation generation changed")
	}
	return nil
}
func (r *Registry) BeginReleasePhase(id string, phase ReleasePhase, revision uint64) (PhaseToken, error) {
	var token PhaseToken
	err := r.registryTransaction(func(d *RegistryData) error {
		o, e := findOperation(d, id)
		if e != nil {
			return e
		}
		if o.Revision != revision {
			return fmt.Errorf("stale release revision")
		}
		if e := verifyOperation(*d, *o); e != nil {
			return e
		}
		for i := range o.Phases {
			p := &o.Phases[i]
			if p.Name == phase {
				if p.State != "pending" && p.State != "blocked" {
					return fmt.Errorf("phase cannot dispatch again")
				}
				p.State = "started"
				p.Code = ""
				p.Attempt = uuid.NewString()
				o.Revision++
				token = PhaseToken{id, phase, p.Attempt}
				return nil
			}
			if p.State != "completed" {
				return fmt.Errorf("prior phase incomplete")
			}
		}
		return fmt.Errorf("phase not required")
	}, true)
	if err != nil {
		return PhaseToken{}, err
	}
	return token, nil
}
func (r *Registry) recordPhase(token PhaseToken, result PhaseResult, retire bool) (ReleaseOperation, error) {
	var output ReleaseOperation
	err := r.registryTransaction(func(d *RegistryData) error {
		o, e := findOperation(d, token.OperationID)
		if e != nil {
			return e
		}
		if !validRequest(o.ReleaseRequest) {
			return fmt.Errorf("legacy release requires retained completion bindings")
		}
		if !validPhaseResult(*o, token.Phase, result) || !canonicalUUID(token.Attempt) {
			return fmt.Errorf("invalid phase result")
		}
		for i := range o.Phases {
			p := &o.Phases[i]
			if p.Name != token.Phase {
				continue
			}
			if p.Attempt != token.Attempt {
				return fmt.Errorf("stale phase attempt")
			}
			if p.State == result.State && p.Code == result.Code {
				output = cloneOperation(*o)
				return nil
			}
			if p.State != "started" {
				return fmt.Errorf("phase result conflict")
			}
			removed := phaseComplete(*o, PhaseWorktree) || token.Phase == PhaseWorktree && result.State == "completed"
			if e := verifyTarget(o.Target, removed); e != nil {
				return e
			}
			if token.Phase == PhaseAllocation && result.State == "completed" {
				if !retire {
					return fmt.Errorf("allocation retirement requires atomic receipt")
				}
				a := allocationAt(*d, o.Target.Path)
				if a == nil || GetString(a, "allocation_generation") != o.Allocation.Generation {
					return fmt.Errorf("allocation generation changed")
				}
				kept := []Allocation{}
				for _, a := range d.Allocations {
					if GetString(a, "allocation_generation") != o.Allocation.Generation {
						kept = append(kept, a)
					}
				}
				d.Allocations = kept
			}
			p.State = result.State
			p.Code = result.Code
			o.Revision++
			output = cloneOperation(*o)
			return nil
		}
		return fmt.Errorf("phase not required")
	}, true)
	if err != nil {
		return ReleaseOperation{}, err
	}
	return output, nil
}
func (r *Registry) RecordReleasePhase(token PhaseToken, result PhaseResult) (ReleaseOperation, error) {
	return r.recordPhase(token, result, false)
}
func (r *Registry) RetireReleaseAllocation(token PhaseToken) (ReleaseOperation, error) {
	if token.Phase != PhaseAllocation {
		return ReleaseOperation{}, fmt.Errorf("not allocation retirement")
	}
	return r.recordPhase(token, PhaseResult{"completed", "ok"}, true)
}

func exactReleaseRecord(raw []byte, record *ReleaseOperation) error {
	if err := uniqueReleaseKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(record); err != nil {
		return err
	}
	typed, err := json.Marshal(record)
	if err != nil {
		return err
	}
	decode := func(b []byte) (any, error) {
		var value any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		err := d.Decode(&value)
		return value, err
	}
	original, err := decode(raw)
	if err != nil {
		return err
	}
	canonical, err := decode(typed)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(original, canonical) {
		return fmt.Errorf("noncanonical release record")
	}
	return nil
}
func allocationGeneration(d RegistryData, g string) Allocation {
	for _, a := range d.Allocations {
		if GetString(a, "allocation_generation") == g {
			return a
		}
	}
	return nil
}
func allocationPath(d RegistryData, p string) Allocation {
	for _, a := range d.Allocations {
		if filepath.Clean(GetString(a, "worktree")) == p {
			return a
		}
	}
	return nil
}

func validPhaseResult(o ReleaseOperation, p ReleasePhase, r PhaseResult) bool {
	return validResult(r) && (r.Code != "ignored_hook_failure" || o.Policy.IgnoreHookFailure && (p == PhasePreHook || p == PhasePostHook))
}

func uniqueReleaseKeys(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var visit func() error
	visit = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				token, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := token.(string)
				if !ok || seen[key] {
					return fmt.Errorf("duplicate release JSON key")
				}
				seen[key] = true
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	return visit()
}
