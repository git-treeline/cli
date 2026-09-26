package proxy

import (
	"slices"
	"strings"
	"testing"
)

// hostilePath carries every shell metacharacter that used to matter when the
// steps were interpolated into a `sudo sh -c` string.
const hostilePath = "/tmp/it's a; `id` $(id) \"dir\"/ca.pem"

func TestLinuxTrustSteps_PassPathsAsLiteralArgv(t *testing.T) {
	cfg := linuxTrustConfig{certDir: "/etc/it's; `x`/anchors", updateCommand: "update-ca-trust"}

	steps := linuxTrustSteps(cfg, hostilePath)
	want := [][]string{
		{"/bin/mkdir", "-p", cfg.certDir},
		{"/bin/cp", hostilePath, cfg.certDir + "/git-treeline.crt"},
		{"update-ca-trust"},
	}
	if !slices.EqualFunc(steps, want, slices.Equal) {
		t.Errorf("trust steps = %q, want %q", steps, want)
	}

	unsteps := linuxUntrustSteps(cfg)
	wantUn := [][]string{
		{"/bin/rm", "-f", cfg.certDir + "/git-treeline.crt"},
		{"update-ca-trust"},
	}
	if !slices.EqualFunc(unsteps, wantUn, slices.Equal) {
		t.Errorf("untrust steps = %q, want %q", unsteps, wantUn)
	}

	for _, argv := range slices.Concat(steps, unsteps) {
		for _, a := range argv {
			if a == "sh" || a == "-c" || strings.Contains(a, " && ") {
				t.Errorf("step %q must not route through a shell", argv)
			}
		}
	}
}

// stubPrivileged replaces the sudo and certutil seams so the Linux trust
// flow can run on any platform without prompting or touching the system.
func stubPrivileged(t *testing.T) *[][]string {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // keep trustNSS away from real NSS databases
	var calls [][]string
	origSudo, origNSS := sudoRunCmd, nssRunCmd
	sudoRunCmd = func(prompt string, argv ...string) error {
		if strings.TrimSpace(prompt) == "" {
			t.Error("sudo step must carry a prompt")
		}
		calls = append(calls, slices.Clone(argv))
		return nil
	}
	nssRunCmd = func(string, ...string) error { return nil }
	t.Cleanup(func() { sudoRunCmd, nssRunCmd = origSudo, origNSS })
	return &calls
}

func TestTrustLinux_NeverInvokesShell(t *testing.T) {
	calls := stubPrivileged(t)
	if err := trustLinux(hostilePath); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 3 {
		t.Fatalf("expected 3 sudo steps, got %d: %q", len(*calls), *calls)
	}
	cp := (*calls)[1]
	if cp[0] != "/bin/cp" || cp[1] != hostilePath {
		t.Errorf("cp must receive the CA path verbatim as one argument, got %q", cp)
	}
	for _, argv := range *calls {
		if slices.Contains(argv, "sh") || slices.Contains(argv, "-c") {
			t.Errorf("sudo step %q routed through a shell", argv)
		}
	}
}

func TestUntrustLinux_NeverInvokesShell(t *testing.T) {
	calls := stubPrivileged(t)
	if err := untrustLinux(); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0][0] != "/bin/rm" {
		t.Fatalf("expected rm then update, got %q", *calls)
	}
	for _, argv := range *calls {
		if slices.Contains(argv, "sh") || slices.Contains(argv, "-c") {
			t.Errorf("sudo step %q routed through a shell", argv)
		}
	}
}
