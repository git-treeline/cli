// Package doctor holds the pure decision logic behind `gtl doctor`: the
// request-flow walk that finds the first broken link in the app→router→pf→CA
// chain, the auto-fix planner that maps findings to remediations, and the
// port-config classifier. Everything here is a pure function over snapshots
// gathered by the cmd layer, so the diagnosis and repair-planning rules can be
// tested without standing up a real router / launchd / pf / CA.
package doctor

import (
	"fmt"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/service"
)

// ClassifyPortConfig checks whether a port base conflicts with the router port
// or is a well-known framework default that should stay free.
// Returns "conflict", "common_dev_port", or "" (ok).
func ClassifyPortConfig(base, routerPort int) string {
	if base == routerPort {
		return "conflict"
	}
	if allocator.IsCommonDevPort(base) {
		return "common_dev_port"
	}
	return ""
}

// FlowStep is one broken link in the request chain: what failed, why, and the
// action that repairs it.
type FlowStep struct {
	Label  string
	Detail string
	Fix    string
}

// FlowInput is the snapshot of state EvaluateRequestFlow examines.
type FlowInput struct {
	AllocatedPorts []int
	AppListening   bool
	StartCommand   string
	ServiceChecks  []service.HealthCheck
	CAInstalled    bool
}

// EvaluateRequestFlow walks the request chain a worktree URL takes (app port
// → router → port forwarding → CA) and returns the FIRST failing link as the
// actionable diagnosis, or nil when every link is healthy. Everything else is
// noise when the first link is broken.
func EvaluateRequestFlow(in FlowInput) *FlowStep {
	// 0. Loopback sanity. If 127.0.0.1 itself is filtered, every downstream
	// "unreachable" verdict (app not listening, router port dead, port 443
	// closed) is a red herring — nothing local can connect regardless of the
	// router's actual health. Surface this first and plainly.
	for _, c := range in.ServiceChecks {
		if c.Name == "loopback" && c.Status == "error" {
			return &FlowStep{
				Label:  "loopback (127.0.0.1)",
				Detail: c.Detail + " — this invalidates the 'unreachable' results below; the router itself may be fine",
				Fix:    c.Fix,
			}
		}
	}

	// 1. App listening on its allocated port?
	if len(in.AllocatedPorts) > 0 && !in.AppListening {
		fix := "start the dev server"
		if in.StartCommand != "" {
			fix = in.StartCommand
		}
		return &FlowStep{
			Label:  fmt.Sprintf("app on :%d", in.AllocatedPorts[0]),
			Detail: "the dev server is not listening — the router has nowhere to forward to",
			Fix:    fix,
		}
	}

	// 2. Router service registered + listening + responding.
	for _, c := range in.ServiceChecks {
		if c.Name == "port_forwarding" {
			continue // handled below
		}
		if c.Status == "error" || (c.Status == "warn" && c.Name == "router_responding") {
			return &FlowStep{
				Label:  c.Name,
				Detail: c.Detail,
				Fix:    c.Fix,
			}
		}
	}
	// router_version mismatch isn't a hard block but is the most likely
	// surprise after a brew upgrade — call it out specifically.
	for _, c := range in.ServiceChecks {
		if c.Name == "router_version" && c.Status == "warn" {
			return &FlowStep{
				Label:  "router_version",
				Detail: c.Detail,
				Fix:    "gtl serve restart",
			}
		}
	}

	// 3. Port forwarding loaded in kernel.
	for _, c := range in.ServiceChecks {
		if c.Name == "port_forwarding" && c.Status != "ok" {
			return &FlowStep{
				Label:  "port_forwarding",
				Detail: c.Detail,
				Fix:    c.Fix,
			}
		}
	}

	// 4. CA cert installed and not expired (browser will warn otherwise,
	// but the request still reaches the router — soft warning only).
	if !in.CAInstalled {
		return &FlowStep{
			Label:  "ca_cert",
			Detail: "CA not installed — browsers will reject HTTPS",
			Fix:    "gtl serve install",
		}
	}
	return nil
}

// FixAction names a remediation that doctor --fix can apply automatically.
type FixAction string

const (
	FixServeRestart FixAction = "gtl serve restart"
	FixReloadPF     FixAction = "gtl serve reload-pf"
	FixPrune        FixAction = "gtl prune"
)

// PlanAutoFix maps the doctor's findings to a deduplicated, ordered list of
// actions to run. Pure function for testability — the caller actually
// executes the actions.
func PlanAutoFix(in FlowInput, registryHasOrphans bool) []FixAction {
	seen := map[FixAction]bool{}
	var plan []FixAction
	add := func(a FixAction) {
		if !seen[a] {
			seen[a] = true
			plan = append(plan, a)
		}
	}

	// First failing link in the request flow drives most fixes.
	step := EvaluateRequestFlow(in)
	if step != nil {
		switch step.Fix {
		case "gtl serve restart":
			add(FixServeRestart)
		case "gtl serve reload-pf":
			add(FixReloadPF)
		}
	}
	// Independent of the flow chain: a stale router-version warning, even if
	// the router is responding, deserves a restart.
	for _, c := range in.ServiceChecks {
		if c.Name == "router_version" && c.Status == "warn" {
			add(FixServeRestart)
		}
	}
	// Registry orphans (entries whose worktree directory is gone) are safe
	// to prune.
	if registryHasOrphans {
		add(FixPrune)
	}
	return plan
}
