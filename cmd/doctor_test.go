package cmd

import (
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/service"
)

func TestClassifyPortConfig_Conflict(t *testing.T) {
	got := classifyPortConfig(8443, 8443)
	if got != "conflict" {
		t.Errorf("classifyPortConfig(8443, 8443) = %q, want %q", got, "conflict")
	}
}

func TestClassifyPortConfig_CommonDevPort(t *testing.T) {
	got := classifyPortConfig(3000, 8443)
	if got != "common_dev_port" {
		t.Errorf("classifyPortConfig(3000, 8443) = %q, want %q", got, "common_dev_port")
	}
}

func TestClassifyPortConfig_Ok(t *testing.T) {
	got := classifyPortConfig(3002, 8443)
	if got != "" {
		t.Errorf("classifyPortConfig(3002, 8443) = %q, want empty", got)
	}
}

func TestClassifyPortConfig_ConflictWhenBaseEqualsRouter(t *testing.T) {
	// Even when base is also a common dev port, equality with router is the conflict
	got := classifyPortConfig(3000, 3000)
	if got != "conflict" {
		t.Errorf("classifyPortConfig(3000, 3000) = %q, want %q", got, "conflict")
	}
}

// healthyChecks returns a healthcheck slice where every link is "ok".
func healthyChecks() []service.HealthCheck {
	return []service.HealthCheck{
		{Name: "service", Status: "ok"},
		{Name: "binary", Status: "ok"},
		{Name: "router_version", Status: "ok"},
		{Name: "router_port", Status: "ok"},
		{Name: "router_responding", Status: "ok"},
		{Name: "port_forwarding", Status: "ok"},
	}
}

func TestEvaluateRequestFlow_AllHealthy(t *testing.T) {
	step := evaluateRequestFlow(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  healthyChecks(),
		caInstalled:    true,
	})
	if step != nil {
		t.Errorf("expected no failing step, got %+v", step)
	}
}

func TestEvaluateRequestFlow_AppNotListening_UsesStartCommand(t *testing.T) {
	step := evaluateRequestFlow(flowInput{
		allocatedPorts: []int{3022},
		appListening:   false,
		startCommand:   "bin/dev",
		serviceChecks:  healthyChecks(),
		caInstalled:    true,
	})
	if step == nil {
		t.Fatal("expected a failing step")
	}
	if !strings.Contains(step.label, "3022") {
		t.Errorf("expected label to include port, got %q", step.label)
	}
	if step.fix != "bin/dev" {
		t.Errorf("expected fix=bin/dev, got %q", step.fix)
	}
}

func TestEvaluateRequestFlow_AppNotListening_NoStartCommand(t *testing.T) {
	step := evaluateRequestFlow(flowInput{
		allocatedPorts: []int{3022},
		appListening:   false,
		serviceChecks:  healthyChecks(),
		caInstalled:    true,
	})
	if step == nil {
		t.Fatal("expected a failing step")
	}
	if step.fix != "start the dev server" {
		t.Errorf("expected generic fix, got %q", step.fix)
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
	step := evaluateRequestFlow(flowInput{
		allocatedPorts: []int{3022},
		appListening:   false,
		startCommand:   "bin/dev",
		serviceChecks:  checks,
		caInstalled:    true,
	})
	if step == nil || !strings.Contains(step.label, "3022") {
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
	step := evaluateRequestFlow(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  checks,
		caInstalled:    false,
	})
	if step == nil || step.label != "router_port" {
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
	step := evaluateRequestFlow(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  checks,
		caInstalled:    true,
	})
	if step == nil {
		t.Fatal("expected pf step")
	}
	if step.label != "port_forwarding" {
		t.Errorf("expected port_forwarding label, got %q", step.label)
	}
	if step.fix != "gtl serve reload-pf" {
		t.Errorf("expected reload-pf fix, got %q", step.fix)
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
	step := evaluateRequestFlow(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  checks,
		caInstalled:    true,
	})
	if step == nil {
		t.Fatal("expected stale-router step")
	}
	if step.label != "router_version" {
		t.Errorf("expected router_version label, got %q", step.label)
	}
	if step.fix != "gtl serve restart" {
		t.Errorf("expected serve restart fix, got %q", step.fix)
	}
}

func TestEvaluateRequestFlow_CANotInstalled(t *testing.T) {
	step := evaluateRequestFlow(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  healthyChecks(),
		caInstalled:    false,
	})
	if step == nil || step.label != "ca_cert" {
		t.Errorf("expected ca_cert step, got %+v", step)
	}
}

// --- planAutoFix ---

func TestPlanAutoFix_NothingToDoOnHealthy(t *testing.T) {
	plan := planAutoFix(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  healthyChecks(),
		caInstalled:    true,
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
	plan := planAutoFix(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  checks,
		caInstalled:    true,
	}, false)
	if len(plan) != 1 || plan[0] != fixServeRestart {
		t.Errorf("expected [fixServeRestart], got %+v", plan)
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
	plan := planAutoFix(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  checks,
		caInstalled:    true,
	}, false)
	found := false
	for _, a := range plan {
		if a == fixReloadPF {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fixReloadPF in plan, got %+v", plan)
	}
}

func TestPlanAutoFix_RegistryOrphansPlanPrune(t *testing.T) {
	plan := planAutoFix(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  healthyChecks(),
		caInstalled:    true,
	}, true)
	found := false
	for _, a := range plan {
		if a == fixPrune {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fixPrune in plan, got %+v", plan)
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
	plan := planAutoFix(flowInput{
		allocatedPorts: []int{3022},
		appListening:   true,
		serviceChecks:  checks,
		caInstalled:    true,
	}, false)
	count := 0
	for _, a := range plan {
		if a == fixServeRestart {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected serve restart once, got %d in %+v", count, plan)
	}
}
