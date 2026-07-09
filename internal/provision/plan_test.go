package provision

import (
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

func kinds(actions []Action) []ActionKind {
	out := make([]ActionKind, len(actions))
	for i, a := range actions {
		out[i] = a.Kind
	}
	return out
}

func TestPlanConfig_OrderAptServicesDatabase(t *testing.T) {
	cfg := config.ProvisionConfig{
		Present:  true,
		Apt:      []string{"libvips"},
		Services: []string{"redis-server"},
		Database: config.ProvisionDatabase{Template: "app_dev", Source: "production"},
	}
	got := kinds(PlanConfig(cfg, "linux"))
	want := []ActionKind{ActionApt, ActionService, ActionDatabase}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}

func TestPlanConfig_DarwinSkipsAptAndServices(t *testing.T) {
	cfg := config.ProvisionConfig{
		Present:  true,
		Apt:      []string{"libvips"},
		Services: []string{"redis-server"},
	}
	actions := PlanConfig(cfg, "darwin")
	for _, a := range actions {
		if a.Kind == ActionApt || a.Kind == ActionService {
			if !a.PlatformSkip {
				t.Errorf("%s should be PlatformSkip on darwin", a.Kind)
			}
		}
	}
}

func TestPlanConfig_DatabaseModeSelection(t *testing.T) {
	cases := []struct {
		name string
		db   config.ProvisionDatabase
		want DBMode
	}{
		{"source wins", config.ProvisionDatabase{Template: "t", Source: "prod", Hydrate: "cmd"}, DBModeSource},
		{"hydrate fallback", config.ProvisionDatabase{Template: "t", Hydrate: "cmd"}, DBModeHydrate},
		{"empty last resort", config.ProvisionDatabase{Template: "t"}, DBModeEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actions := PlanConfig(config.ProvisionConfig{Present: true, Database: tc.db}, "linux")
			if len(actions) != 1 || actions[0].Kind != ActionDatabase {
				t.Fatalf("expected one database action, got %v", kinds(actions))
			}
			if actions[0].DBMode != tc.want {
				t.Errorf("mode = %q, want %q", actions[0].DBMode, tc.want)
			}
		})
	}
}

func TestPlanConfig_NoTemplateNoDatabaseAction(t *testing.T) {
	cfg := config.ProvisionConfig{Present: true, Apt: []string{"x"}}
	for _, a := range PlanConfig(cfg, "linux") {
		if a.Kind == ActionDatabase {
			t.Error("no database action expected without a template")
		}
	}
}

func TestBuildPlan_RuntimeInsertedBeforeDatabase(t *testing.T) {
	cfg := config.ProvisionConfig{
		Present:  true,
		Apt:      []string{"libvips"},
		Database: config.ProvisionDatabase{Template: "app_dev"},
	}
	fe := func(p string) bool { return hasSuffix(p, ".ruby-version") }
	got := kinds(BuildPlan(cfg, "/repo", "linux", fe))
	want := []ActionKind{ActionApt, ActionRuntime, ActionDatabase}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}

func TestBuildPlan_RuntimeAppendedWhenNoDatabase(t *testing.T) {
	cfg := config.ProvisionConfig{Present: true, Apt: []string{"libvips"}}
	fe := func(p string) bool { return hasSuffix(p, ".tool-versions") }
	got := kinds(BuildPlan(cfg, "/repo", "linux", fe))
	want := []ActionKind{ActionApt, ActionRuntime}
	if len(got) != len(want) || got[1] != ActionRuntime {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}

func TestRuntimeAction_NoVersionFile_Nil(t *testing.T) {
	if RuntimeAction("/repo", func(string) bool { return false }) != nil {
		t.Error("expected nil runtime action when no version files present")
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
