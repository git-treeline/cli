package doctor

import (
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/service"
)

func TestClassifyPortConfig_Conflict(t *testing.T) {
	got := ClassifyPortConfig(8443, 8443)
	if got != "conflict" {
		t.Errorf("ClassifyPortConfig(8443, 8443) = %q, want %q", got, "conflict")
	}
}

func TestClassifyPortConfig_CommonDevPort(t *testing.T) {
	got := ClassifyPortConfig(3000, 8443)
	if got != "common_dev_port" {
		t.Errorf("ClassifyPortConfig(3000, 8443) = %q, want %q", got, "common_dev_port")
	}
}

func TestClassifyPortConfig_Ok(t *testing.T) {
	got := ClassifyPortConfig(3002, 8443)
	if got != "" {
		t.Errorf("ClassifyPortConfig(3002, 8443) = %q, want empty", got)
	}
}

func TestClassifyPortConfig_ConflictWhenBaseEqualsRouter(t *testing.T) {
	// Even when base is also a common dev port, equality with router is the conflict
	got := ClassifyPortConfig(3000, 3000)
	if got != "conflict" {
		t.Errorf("ClassifyPortConfig(3000, 3000) = %q, want %q", got, "conflict")
	}
}

// healthyChecks returns a healthcheck slice where every link is "ok".
func healthyChecks() []service.HealthCheck {
	return []service.HealthCheck{
		{Name: "loopback", Status: "ok"},
		{Name: "service", Status: "ok"},
		{Name: "binary", Status: "ok"},
		{Name: "router_version", Status: "ok"},
		{Name: "router_port", Status: "ok"},
		{Name: "router_responding", Status: "ok"},
		{Name: "port_forwarding", Status: "ok"},
	}
}

func TestEvaluateRequestFlow_AllHealthy(t *testing.T) {
	step := EvaluateRequestFlow(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  healthyChecks(),
		CAInstalled:    true,
	})
	if step != nil {
		t.Errorf("expected no failing step, got %+v", step)
	}
}

// A broken loopback must reframe every downstream "unreachable" verdict —
// even an app that isn't listening is a red herring when 127.0.0.1 is filtered.
func TestEvaluateRequestFlow_BrokenLoopbackReframesEverything(t *testing.T) {
	checks := healthyChecks()
	for i := range checks {
		if checks[i].Name == "loopback" {
			checks[i].Status = "error"
			checks[i].Detail = "loopback is being filtered"
			checks[i].Fix = "allow loopback traffic"
		}
	}
	step := EvaluateRequestFlow(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   false, // would normally win, but loopback outranks it
		StartCommand:   "bin/dev",
		ServiceChecks:  checks,
		CAInstalled:    true,
	})
	if step == nil {
		t.Fatal("expected a failing step")
	}
	if !strings.Contains(step.Label, "loopback") {
		t.Errorf("expected loopback to be surfaced first, got %q", step.Label)
	}
	if step.Fix != "allow loopback traffic" {
		t.Errorf("expected loopback fix, got %q", step.Fix)
	}
}

func TestEvaluateRequestFlow_AppNotListening_UsesStartCommand(t *testing.T) {
	step := EvaluateRequestFlow(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   false,
		StartCommand:   "bin/dev",
		ServiceChecks:  healthyChecks(),
		CAInstalled:    true,
	})
	if step == nil {
		t.Fatal("expected a failing step")
	}
	if !strings.Contains(step.Label, "3022") {
		t.Errorf("expected label to include port, got %q", step.Label)
	}
	if step.Fix != "bin/dev" {
		t.Errorf("expected fix=bin/dev, got %q", step.Fix)
	}
}

func TestEvaluateRequestFlow_AppNotListening_NoStartCommand(t *testing.T) {
	step := EvaluateRequestFlow(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   false,
		ServiceChecks:  healthyChecks(),
		CAInstalled:    true,
	})
	if step == nil {
		t.Fatal("expected a failing step")
	}
	if step.Fix != "start the dev server" {
		t.Errorf("expected generic fix, got %q", step.Fix)
	}
}

// App-not-listening must take precedence over a stale router — the user
// won't see anything regardless of router state if their backend is dead.
func TestEvaluateRequestFlow_AppLossBeatsRouterStale(t *testing.T) {
	checks := healthyChecks()
	for i := range checks {
		if checks[i].Name == "router_version" {
			checks[i].Status = "warn"
			checks[i].Detail = "router=0.39.2, cli=0.39.4"
		}
	}
	step := EvaluateRequestFlow(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   false,
		StartCommand:   "bin/dev",
		ServiceChecks:  checks,
		CAInstalled:    true,
	})
	if step == nil || !strings.Contains(step.Label, "3022") {
		t.Errorf("expected app step first, got %+v", step)
	}
}

func TestEvaluateRequestFlow_RouterErrorBeatsPFAndCA(t *testing.T) {
	checks := healthyChecks()
	for i := range checks {
		if checks[i].Name == "router_port" {
			checks[i].Status = "error"
			checks[i].Detail = "port not listening"
			checks[i].Fix = "gtl serve restart"
		}
		if checks[i].Name == "port_forwarding" {
			checks[i].Status = "error"
			checks[i].Detail = "rule not loaded"
			checks[i].Fix = "gtl serve reload-pf"
		}
	}
	step := EvaluateRequestFlow(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  checks,
		CAInstalled:    false,
	})
	if step == nil || step.Label != "router_port" {
		t.Errorf("expected router_port first, got %+v", step)
	}
}

func TestEvaluateRequestFlow_PFFailureSurfaced(t *testing.T) {
	checks := healthyChecks()
	for i := range checks {
		if checks[i].Name == "port_forwarding" {
			checks[i].Status = "error"
			checks[i].Detail = "rule not loaded in kernel"
			checks[i].Fix = "gtl serve reload-pf"
		}
	}
	step := EvaluateRequestFlow(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  checks,
		CAInstalled:    true,
	})
	if step == nil {
		t.Fatal("expected pf step")
	}
	if step.Label != "port_forwarding" {
		t.Errorf("expected port_forwarding label, got %q", step.Label)
	}
	if step.Fix != "gtl serve reload-pf" {
		t.Errorf("expected reload-pf fix, got %q", step.Fix)
	}
}

func TestEvaluateRequestFlow_RouterStaleSurfaced(t *testing.T) {
	checks := healthyChecks()
	for i := range checks {
		if checks[i].Name == "router_version" {
			checks[i].Status = "warn"
			checks[i].Detail = "router=0.39.2, cli=0.39.4"
		}
	}
	step := EvaluateRequestFlow(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  checks,
		CAInstalled:    true,
	})
	if step == nil {
		t.Fatal("expected stale-router step")
	}
	if step.Label != "router_version" {
		t.Errorf("expected router_version label, got %q", step.Label)
	}
	if step.Fix != "gtl serve restart" {
		t.Errorf("expected serve restart fix, got %q", step.Fix)
	}
}

func TestEvaluateRequestFlow_CANotInstalled(t *testing.T) {
	step := EvaluateRequestFlow(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  healthyChecks(),
		CAInstalled:    false,
	})
	if step == nil || step.Label != "ca_cert" {
		t.Errorf("expected ca_cert step, got %+v", step)
	}
}

// --- PlanAutoFix ---

func TestPlanAutoFix_NothingToDoOnHealthy(t *testing.T) {
	plan := PlanAutoFix(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  healthyChecks(),
		CAInstalled:    true,
	}, false)
	if len(plan) != 0 {
		t.Errorf("expected empty plan, got %+v", plan)
	}
}

func TestPlanAutoFix_StaleRouterPlansRestart(t *testing.T) {
	checks := healthyChecks()
	for i := range checks {
		if checks[i].Name == "router_version" {
			checks[i].Status = "warn"
			checks[i].Detail = "router=0.39.2, cli=0.39.4"
		}
	}
	plan := PlanAutoFix(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  checks,
		CAInstalled:    true,
	}, false)
	if len(plan) != 1 || plan[0] != FixServeRestart {
		t.Errorf("expected [FixServeRestart], got %+v", plan)
	}
}

func TestPlanAutoFix_PFErrorPlansReload(t *testing.T) {
	checks := healthyChecks()
	for i := range checks {
		if checks[i].Name == "port_forwarding" {
			checks[i].Status = "error"
			checks[i].Fix = "gtl serve reload-pf"
		}
	}
	plan := PlanAutoFix(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  checks,
		CAInstalled:    true,
	}, false)
	found := false
	for _, a := range plan {
		if a == FixReloadPF {
			found = true
		}
	}
	if !found {
		t.Errorf("expected FixReloadPF in plan, got %+v", plan)
	}
}

func TestPlanAutoFix_RegistryOrphansPlanPrune(t *testing.T) {
	plan := PlanAutoFix(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  healthyChecks(),
		CAInstalled:    true,
	}, true)
	found := false
	for _, a := range plan {
		if a == FixPrune {
			found = true
		}
	}
	if !found {
		t.Errorf("expected FixPrune in plan, got %+v", plan)
	}
}

func TestPlanAutoFix_DedupesMultipleSignals(t *testing.T) {
	// Both router_version warn AND router_responding warn should
	// produce a SINGLE serve restart action, not two.
	checks := healthyChecks()
	for i := range checks {
		if checks[i].Name == "router_version" {
			checks[i].Status = "warn"
		}
		if checks[i].Name == "router_responding" {
			checks[i].Status = "warn"
			checks[i].Fix = "gtl serve restart"
		}
	}
	plan := PlanAutoFix(FlowInput{
		AllocatedPorts: []int{3022},
		AppListening:   true,
		ServiceChecks:  checks,
		CAInstalled:    true,
	}, false)
	count := 0
	for _, a := range plan {
		if a == FixServeRestart {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected serve restart once, got %d in %+v", count, plan)
	}
}
