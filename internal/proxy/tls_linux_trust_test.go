package proxy

import (
	"slices"
	"strings"
	"testing"
)

func TestLinuxUntrustSteps_PassPathsAsLiteralArgv(t *testing.T) {
	cfg := linuxTrustConfig{certDir: "/etc/it's; `x`/anchors", updateCommand: "/usr/bin/update-ca-trust"}
	unsteps := linuxUntrustSteps(cfg)
	wantUn := [][]string{
		{"/bin/rm", "-f", cfg.certDir + "/git-treeline.crt"},
		{"/usr/bin/update-ca-trust"},
	}
	if !slices.EqualFunc(unsteps, wantUn, slices.Equal) {
		t.Errorf("untrust steps = %q, want %q", unsteps, wantUn)
	}

	for _, argv := range unsteps {
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

func TestTrustLinux_StagesCapturedCertificateBytes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "") // keep the test away from real certutil/NSS stores
	certPEM := []byte("captured CA bytes with 'quotes' and $(commands)")
	var prompt, script string
	orig := privilegedScriptRunner
	privilegedScriptRunner = func(gotPrompt, gotScript string) error {
		prompt, script = gotPrompt, gotScript
		return nil
	}
	t.Cleanup(func() { privilegedScriptRunner = orig })

	if err := trustLinux(certPEM); err != nil {
		t.Fatal(err)
	}
	updateCommand := linuxTrustConfigs[detectLinuxDistro()].updateCommand
	for _, want := range []string{"captured CA bytes", `"$stage/ca.pem"`, "/usr/bin/install -m 0644", updateCommand} {
		if !strings.Contains(script, want) {
			t.Errorf("privileged script missing %q:\n%s", want, script)
		}
	}
	if strings.TrimSpace(prompt) == "" {
		t.Error("privileged script must carry a password prompt")
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
