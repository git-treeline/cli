package cmd

import "testing"

// TestLinkCmd_RestartFlagIsDeprecatedNoop confirms --restart still parses
// (so old invocations don't break) but is marked deprecated/hidden — it no
// longer gates anything, since RunE always restarts the server when running.
func TestLinkCmd_RestartFlagIsDeprecatedNoop(t *testing.T) {
	f := linkCmd.Flags().Lookup("restart")
	if f == nil {
		t.Fatal("expected --restart flag to still be registered on link")
	}
	if f.Deprecated == "" {
		t.Error("expected --restart to be marked deprecated")
	}
	if !f.Hidden {
		t.Error("expected --restart to be hidden (MarkDeprecated should imply this)")
	}
}
