package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The binary is installed as 'gtl' (a symlink), so the generated completion
// scripts must register the 'gtl' command name — not the underlying
// 'git-treeline' binary name — or 'gtl <tab>' does nothing.
func TestCompletionTargetsGtl(t *testing.T) {
	cases := []struct {
		shell string
		want  string
	}{
		{"zsh", "#compdef gtl"},
		{"bash", "# bash completion for gtl"},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			orig := os.Stdout
			os.Stdout = w
			runErr := completionCmd.RunE(completionCmd, []string{tc.shell})
			_ = w.Close()
			os.Stdout = orig

			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)
			out := buf.String()

			if runErr != nil {
				t.Fatalf("completion %s failed: %v", tc.shell, runErr)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("completion %s output missing %q", tc.shell, tc.want)
			}
			if strings.Contains(out, "git-treeline") {
				t.Errorf("completion %s output still references git-treeline", tc.shell)
			}
		})
	}

	// The override must be restored so help/usage output is unaffected.
	if got := rootCmd.Use; got != "git-treeline" {
		t.Errorf("rootCmd.Use = %q after completion; want git-treeline (override not restored)", got)
	}
}
