package templates

import (
	"testing"

	"github.com/git-treeline/cli/internal/detect"
)

func TestFrameworkName(t *testing.T) {
	tests := []struct {
		framework string
		want      string
	}{
		{"nextjs", "Next.js"},
		{"vite", "Vite"},
		{"rails", "Rails"},
		{"django", "Django"},
		{"node", "Node.js"},
		{"go", "go"},
		{"", ""},
		{"custom", "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.framework, func(t *testing.T) {
			det := &detect.Result{Framework: tt.framework}
			got := frameworkName(det)
			if got != tt.want {
				t.Errorf("frameworkName(%q) = %q, want %q", tt.framework, got, tt.want)
			}
		})
	}
}

func TestDiagnose_RailsAutoLoadsEnv(t *testing.T) {
	det := &detect.Result{
		Framework: "rails",
	}
	diags := Diagnose(det)
	found := false
	for _, d := range diags {
		if d.Level == "info" && contains(d.Message, "Rails") && contains(d.Message, "auto-loads") {
			found = true
		}
	}
	if !found {
		t.Error("expected Rails auto-load diagnostic")
	}
}

func TestDiagnose_NodeNoDotenv(t *testing.T) {
	det := &detect.Result{
		Framework: "node",
	}
	diags := Diagnose(det)
	found := false
	for _, d := range diags {
		if d.Level == "warn" && contains(d.Message, "dotenv") {
			found = true
		}
	}
	if !found {
		t.Error("expected dotenv warning for node without dotenv")
	}
}

func TestDiagnose_PythonNoDotenv(t *testing.T) {
	det := &detect.Result{
		Framework: "python",
	}
	diags := Diagnose(det)
	found := false
	for _, d := range diags {
		if d.Level == "warn" && contains(d.Message, "python-dotenv") {
			found = true
		}
	}
	if !found {
		t.Error("expected python-dotenv warning")
	}
}

func TestDiagnose_GoSourceHint(t *testing.T) {
	det := &detect.Result{
		Framework: "go",
	}
	diags := Diagnose(det)
	found := false
	for _, d := range diags {
		if d.Level == "info" && contains(d.Message, "Source it") {
			found = true
		}
	}
	if !found {
		t.Error("expected source hint for Go")
	}
}

func TestDiagnose_WithEnvFile(t *testing.T) {
	det := &detect.Result{
		Framework:  "rails",
		HasEnvFile: true,
		EnvFile:    ".env.local",
	}
	diags := Diagnose(det)
	found := false
	for _, d := range diags {
		if d.Level == "info" && contains(d.Message, ".env.local") {
			found = true
		}
	}
	if !found {
		t.Error("expected env file found diagnostic")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
