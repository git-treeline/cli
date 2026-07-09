package cmd

import (
	"encoding/json"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

func TestRenderValidateJSON_Clean(t *testing.T) {
	var runErr error
	out := captureStdout(t, func() {
		runErr = renderValidateJSON(registryValidateCmd, nil)
	})
	if runErr != nil {
		t.Fatalf("clean registry should not error, got: %v", runErr)
	}

	var report validateReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if !report.Healthy || report.Count != 0 || len(report.Findings) != 0 {
		t.Errorf("unexpected clean report: %+v", report)
	}
}

func TestRenderValidateJSON_WithIssues(t *testing.T) {
	issues := []registry.Issue{
		{Kind: "missing_worktree", Detail: "directory gone", Fix: "gtl prune"},
		{Kind: "duplicate_port", Detail: "port 3000 held twice"},
	}
	var runErr error
	out := captureStdout(t, func() {
		runErr = renderValidateJSON(registryValidateCmd, issues)
	})
	if runErr == nil {
		t.Fatal("expected a non-nil error to drive exit code 1 when issues exist")
	}

	var report validateReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if report.Healthy {
		t.Error("report should be unhealthy")
	}
	if report.Count != 2 || len(report.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %+v", report)
	}
	if report.Findings[0].Kind != "missing_worktree" || report.Findings[0].Fix != "gtl prune" {
		t.Errorf("unexpected first finding: %+v", report.Findings[0])
	}
	if report.Findings[1].Fix != "" {
		t.Errorf("empty fix should be omitted, got %q", report.Findings[1].Fix)
	}
}
