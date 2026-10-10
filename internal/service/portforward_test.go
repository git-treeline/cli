package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/platform"
)

func TestDarwinInstallDoesNotBootstrapAfterLoadFailure(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "bootstrapped")
	var replacements []string
	for command, body := range map[string]string{
		"/bin/mkdir":      "exit 0",
		"/bin/cp":         "exit 0",
		"/bin/chmod":      "exit 0",
		"/usr/sbin/chown": "exit 0",
		"/sbin/pfctl":     "case \"$1\" in -f) exit 7;; *) exit 0;; esac",
		"/bin/launchctl":  "printf reached > " + platform.ShellQuote(marker),
	} {
		stub := filepath.Join(dir, filepath.Base(command))
		if err := os.WriteFile(stub, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		replacements = append(replacements, command, platform.ShellQuote(stub))
	}
	// Every privileged executable is replaced; the generated control flow runs
	// against fixtures only, without sudo or system-file changes.
	script := strings.NewReplacer(replacements...).Replace(darwinPortForwardScript("anchor", "conf", "plist", "script"))
	cmd := exec.Command("/bin/sh", "-c", "set -e\n"+script)
	if err := cmd.Run(); err == nil {
		t.Fatal("firewall load failure was masked")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("daemon bootstrapped despite failed rule load: %v", err)
	}
}

const samplePfConf = `scrub-anchor "com.apple/*"
nat-anchor "com.apple/*"
rdr-anchor "com.apple/*"
dummynet-anchor "com.apple/*"
anchor "com.apple/*"
load anchor "com.apple" from "/etc/pf.anchors/com.apple"
`

func TestInsertPfRules(t *testing.T) {
	result := insertPfRules(samplePfConf)

	if !strings.Contains(result, `rdr-anchor "dev.treeline.router"`) {
		t.Error("missing rdr-anchor line")
	}
	if !strings.Contains(result, `load anchor "dev.treeline.router"`) {
		t.Error("missing load anchor line")
	}

	lines := strings.Split(result, "\n")
	rdrAppleIdx := -1
	rdrTreelineIdx := -1
	anchorIdx := -1
	loadIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == `rdr-anchor "com.apple/*"` {
			rdrAppleIdx = i
		}
		if strings.Contains(trimmed, `rdr-anchor "dev.treeline.router"`) {
			rdrTreelineIdx = i
		}
		if trimmed == `anchor "com.apple/*"` {
			anchorIdx = i
		}
		if strings.Contains(trimmed, `load anchor "dev.treeline.router"`) {
			loadIdx = i
		}
	}

	if rdrTreelineIdx <= rdrAppleIdx {
		t.Errorf("rdr-anchor should come after com.apple rdr-anchor (apple=%d, treeline=%d)", rdrAppleIdx, rdrTreelineIdx)
	}
	if rdrTreelineIdx >= anchorIdx {
		t.Errorf("rdr-anchor should come before anchor line (treeline=%d, anchor=%d)", rdrTreelineIdx, anchorIdx)
	}
	if loadIdx <= anchorIdx {
		t.Errorf("load anchor should come at end (load=%d, anchor=%d)", loadIdx, anchorIdx)
	}
}

func TestInsertPfRules_NoExistingRdrAnchor(t *testing.T) {
	minimal := "anchor \"com.apple/*\"\n"
	result := insertPfRules(minimal)

	if !strings.Contains(result, `rdr-anchor "dev.treeline.router"`) {
		t.Error("missing rdr-anchor line")
	}

	lines := strings.Split(result, "\n")
	if !strings.Contains(lines[0], `rdr-anchor "dev.treeline.router"`) {
		t.Error("rdr-anchor should be inserted at the top when no existing rdr-anchor")
	}
}

func TestInsertPfRules_MarkerPresent(t *testing.T) {
	result := insertPfRules(samplePfConf)
	count := strings.Count(result, pfMarker())
	if count != 2 {
		t.Errorf("expected 2 marker comments (rdr-anchor + load anchor), got %d", count)
	}
}

func TestInsertPfRules_TrailingNewline(t *testing.T) {
	result := insertPfRules(samplePfConf)
	if !strings.HasSuffix(result, "\n") {
		t.Error("pf.conf must end with a newline — pfctl will reject it otherwise")
	}
}

func TestGeneratePfAnchor(t *testing.T) {
	content := GeneratePfAnchor(3001)
	if !strings.Contains(content, "rdr pass on lo0") {
		t.Error("missing rdr rule")
	}
	if !strings.Contains(content, "port 443") {
		t.Error("should redirect from port 443")
	}
	if !strings.Contains(content, "port 3001") {
		t.Error("should redirect to port 3001")
	}
}

func TestDarwinPortForwardScript(t *testing.T) {
	script := darwinPortForwardScript(
		"/tmp/treeline-anchor-xyz",
		"/tmp/treeline-pfconf-xyz",
		"/tmp/treeline-pfreload-xyz.plist",
		"/tmp/treeline-pf-ensure-xyz.sh",
	)

	// Invariant 1: dry-run validation gates the live pf.conf swap. The
	// validation must use `|| exit 1` so a broken pf.conf cannot be
	// installed under any condition.
	if !strings.Contains(script, "/sbin/pfctl -n -f '/tmp/treeline-pfconf-xyz' 2>&1 || exit 1") {
		t.Errorf("script must gate validation with `|| exit 1` so a bad pf.conf cannot be installed\nscript: %s", script)
	}

	// Loading failures must stop installation; only an already-enabled pf is tolerated.
	if !strings.Contains(script, "/sbin/pfctl -f '/etc/pf.conf' 2>/dev/null && { /sbin/pfctl -e 2>/dev/null || true; }") {
		t.Errorf("load failure must propagate before enabling pf: %s", script)
	}

	// Invariant 3 (the whole point of the fix): daemon fragment is
	// `&&`-joined at the tail so its exit code is the script's exit code.
	// A `;` here would silently mask daemon-install failures and reopen
	// the issue #51 bug.
	daemonFrag := pfReloadDaemonInstallFragment(
		"/tmp/treeline-pfreload-xyz.plist", "/tmp/treeline-pf-ensure-xyz.sh")
	if !strings.HasSuffix(strings.TrimSpace(script), daemonFrag) {
		t.Errorf("daemon fragment must be the final segment so its exit code drives the script\nscript: %s", script)
	}
	if !strings.Contains(script, "true; } && "+daemonFrag[:30]) {
		t.Errorf("daemon fragment must be joined to the pfctl enable mask with `&&`, not `;` — else daemon failure is silently swallowed\nscript: %s", script)
	}
}

func TestReloadPfAndInstallDaemonScript(t *testing.T) {
	script := reloadPfAndInstallDaemonScript(
		"/tmp/treeline-pfreload-xyz.plist", "/tmp/treeline-pf-ensure-xyz.sh")

	// `pfctl -f` (load) failure MUST propagate — a broken pf.conf means
	// the daemon would re-apply broken rules every boot. Only `pfctl -e`
	// should be masked (it returns non-zero when pf is already enabled).
	if !strings.Contains(script, "/sbin/pfctl -f '/etc/pf.conf' 2>/dev/null && ") {
		t.Errorf("pfctl -f failure must propagate via `&&`, not be masked\nscript: %s", script)
	}
	if !strings.Contains(script, "{ /sbin/pfctl -e 2>/dev/null || true; }") {
		t.Errorf("only pfctl -e should be masked via brace group\nscript: %s", script)
	}

	// Daemon fragment is the exit gate.
	daemonFrag := pfReloadDaemonInstallFragment(
		"/tmp/treeline-pfreload-xyz.plist", "/tmp/treeline-pf-ensure-xyz.sh")
	if !strings.HasSuffix(strings.TrimSpace(script), daemonFrag) {
		t.Errorf("daemon fragment must be the final segment\nscript: %s", script)
	}
	if !strings.Contains(script, "true; } && "+daemonFrag[:30]) {
		t.Errorf("daemon fragment must be `&&`-joined, not `;`-joined\nscript: %s", script)
	}
}
