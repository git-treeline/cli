// Package github provides GitHub API integration via the gh CLI.
// It supports looking up pull request metadata such as branch names
// to enable the "gtl review <PR#>" workflow.
package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type PRInfo struct {
	HeadRefName string `json:"headRefName"`
}

// LookupPR fetches PR metadata using the gh CLI. Returns the head branch name.
func LookupPR(number int) (*PRInfo, error) {
	if err := checkGH(); err != nil {
		return nil, err
	}

	cmd := exec.Command("gh", "pr", "view", fmt.Sprintf("%d", number),
		"--json", "headRefName")
	out, err := cmd.CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		if strings.Contains(output, "Could not resolve") || strings.Contains(output, "not found") {
			return nil, fmt.Errorf("PR #%d not found in this repository", number)
		}
		return nil, fmt.Errorf("gh pr view failed: %s", output)
	}

	var info PRInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parsing gh output: %w", err)
	}
	if info.HeadRefName == "" {
		return nil, fmt.Errorf("PR #%d has no head branch", number)
	}
	return &info, nil
}

func checkGH() error {
	_, err := exec.LookPath("gh")
	if err != nil {
		return fmt.Errorf("gh CLI required: install from https://cli.github.com")
	}
	return nil
}
