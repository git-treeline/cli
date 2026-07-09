package service

import (
	"strings"
	"testing"
)

func TestNonSystemdInstallMessage_WSL(t *testing.T) {
	msg := nonSystemdInstallMessage(true)
	for _, want := range []string{"WSL2", "systemd=true", "/etc/wsl.conf", "gtl serve run"} {
		if !strings.Contains(msg, want) {
			t.Errorf("WSL message missing %q\nmsg: %s", want, msg)
		}
	}
}

func TestNonSystemdInstallMessage_Generic(t *testing.T) {
	msg := nonSystemdInstallMessage(false)
	if strings.Contains(msg, "WSL") {
		t.Errorf("generic message should not mention WSL\nmsg: %s", msg)
	}
	if !strings.Contains(msg, "gtl serve run") {
		t.Errorf("generic message should offer the foreground fallback\nmsg: %s", msg)
	}
}
