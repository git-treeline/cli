package cmd

import (
	"testing"
)

func TestInferDirectory_ExplicitArg(t *testing.T) {
	got := inferDirectory("https://github.com/org/repo.git", []string{"mydir"})
	if got != "mydir" {
		t.Errorf("expected 'mydir', got %q", got)
	}
}

func TestInferDirectory_FromURL(t *testing.T) {
	got := inferDirectory("https://github.com/org/myrepo.git", nil)
	if got != "myrepo" {
		t.Errorf("expected 'myrepo', got %q", got)
	}
}

func TestInferDirectory_FromURLNoGitSuffix(t *testing.T) {
	got := inferDirectory("https://github.com/org/myrepo", nil)
	if got != "myrepo" {
		t.Errorf("expected 'myrepo', got %q", got)
	}
}

func TestInferDirectory_SkipsFlags(t *testing.T) {
	got := inferDirectory("https://github.com/org/repo.git", []string{"--depth", "1", "custom-dir"})
	if got != "custom-dir" {
		t.Errorf("expected 'custom-dir', got %q", got)
	}
}

func TestInferDirectory_SkipsBoolFlags(t *testing.T) {
	got := inferDirectory("https://github.com/org/repo.git", []string{"--recursive", "target"})
	if got != "target" {
		t.Errorf("expected 'target', got %q", got)
	}
}

func TestInferDirectory_OnlyFlags(t *testing.T) {
	got := inferDirectory("https://github.com/org/repo.git", []string{"--depth", "1"})
	if got != "repo" {
		t.Errorf("expected 'repo' from URL, got %q", got)
	}
}

func TestParseCloneDestination(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{
			name:   "standard output",
			stderr: "Cloning into 'myrepo'...\nremote: counting objects...\n",
			want:   "myrepo",
		},
		{
			name:   "double quotes",
			stderr: `Cloning into "myrepo"...`,
			want:   "myrepo",
		},
		{
			name:   "no match",
			stderr: "some other output\n",
			want:   "",
		},
		{
			name:   "empty",
			stderr: "",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCloneDestination(tt.stderr)
			if got != tt.want {
				t.Errorf("parseCloneDestination(%q) = %q, want %q", tt.stderr, got, tt.want)
			}
		})
	}
}
