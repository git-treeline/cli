package cmd

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"

	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
)

// Domain is supplied by the caller's validated configuration. This adapter
// never loads or migrates config and never elevates privileges.
type releaseHostsAdapter struct {
	Registry *registry.Registry
	Domain   string
	Read     func() (service.ManagedHostsSnapshot, error)
	Write    func(service.ManagedHostsSnapshot, []string) error
}

func (a releaseHostsAdapter) Run(ctx context.Context, op registry.ReleaseOperation) registry.PhaseResult {
	blocked := registry.PhaseResult{State: "blocked", Code: "failed"}
	unknown := registry.PhaseResult{State: "uncertain", Code: "unknown"}
	complete := registry.PhaseResult{State: "completed", Code: "ok"}
	if a.Registry == nil || ctx.Err() != nil {
		return blocked
	}
	current, err := a.Registry.GetRelease(op.OperationID)
	if err != nil || current == nil || !reflect.DeepEqual(*current, op) || op.Snapshot == nil {
		return blocked
	}
	retired, started := false, false
	for _, phase := range op.Phases {
		if phase.Name == registry.PhaseAllocation {
			retired = phase.State == "completed"
		}
		if phase.Name == registry.PhaseHosts {
			started = phase.State == "started"
		}
	}
	if !retired || !started {
		return blocked
	}
	domain := strings.ToLower(a.Domain)
	if !releaseHostsDomainValid(domain) {
		return registry.PhaseResult{State: "blocked", Code: "config_changed"}
	}
	desired, err := a.desired(domain)
	if err != nil {
		return blocked
	}
	read := a.Read
	if read == nil {
		read = func() (service.ManagedHostsSnapshot, error) { return service.ReadManagedHostsStrict("") }
	}
	snapshot, err := read()
	if err != nil {
		return blocked
	}
	managed := normalizedReleaseHosts(snapshot.Hostnames())
	if op.Snapshot.Project == "" || len(managed) == 0 {
		return complete
	}
	released := strings.ToLower(proxy.RouteKey(op.Snapshot.Project, op.Snapshot.Branch) + "." + domain)
	if !releaseHostsDomainValid(released) {
		return registry.PhaseResult{State: "blocked", Code: "config_changed"}
	}
	if !slices.Contains(managed, released) || slices.Equal(managed, desired) {
		return complete
	}
	if ctx.Err() != nil {
		return blocked
	}
	write := a.Write
	if write == nil {
		write = service.SyncManagedHostsNoninteractive
	}
	if err := write(snapshot, append([]string(nil), desired...)); err != nil {
		switch {
		case errors.Is(err, service.ErrHostsPrivilegeRequired):
			return registry.PhaseResult{State: "blocked", Code: "privilege_required"}
		case errors.Is(err, service.ErrHostsChanged), errors.Is(err, service.ErrHostsBusy):
			return blocked
		default:
			return unknown
		}
	}
	verified, err := read()
	if err != nil || !slices.Equal(normalizedReleaseHosts(verified.Hostnames()), desired) {
		return unknown
	}
	// Do not certify an obsolete survivor set if another allocation changed
	// during this shared-file effect. Other lifecycle writers remain a rollout
	// prerequisite; this check does not claim to lock arbitrary hosts editors.
	latest, err := a.desired(domain)
	if err != nil || !slices.Equal(latest, desired) {
		return unknown
	}
	return complete
}

func (a releaseHostsAdapter) desired(domain string) ([]string, error) {
	return a.desiredExcluding(domain, nil)
}

func (a releaseHostsAdapter) desiredExcluding(domain string, retiring *registry.ReleaseOperation) ([]string, error) {
	allocs, err := a.Registry.ReleasePreflight()
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, alloc := range allocs {
		if retiring != nil && retiring.Allocation.Kind == "allocated" && registry.GetString(alloc, "worktree") == retiring.Target.Path && registry.GetString(alloc, "allocation_generation") == retiring.Allocation.Generation {
			continue
		}
		project, projectOK := alloc["project"].(string)
		branch, branchOK := alloc["branch"].(string)
		_, hasProject := alloc["project"]
		_, hasBranch := alloc["branch"]
		if hasProject && !projectOK || hasBranch && !branchOK {
			return nil, errors.New("invalid routing allocation")
		}
		if project == "" {
			continue
		}
		host := strings.ToLower(proxy.RouteKey(project, branch) + "." + domain)
		if !releaseHostsDomainValid(host) {
			return nil, errors.New("invalid route hostname")
		}
		hosts = append(hosts, host)
	}
	return normalizedReleaseHosts(hosts), nil
}

func releaseHostsDomainValid(domain string) bool {
	if len(domain) > 253 {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if proxy.ValidateAliasName(label) != nil {
			return false
		}
	}
	return true
}

func normalizedReleaseHosts(hosts []string) []string {
	for i := range hosts {
		hosts[i] = strings.ToLower(hosts[i])
	}
	slices.Sort(hosts)
	return slices.Compact(hosts)
}
