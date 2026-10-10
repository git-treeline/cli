//go:build darwin

package supervisor

import (
	"net"
	"os"
	"syscall"
	"testing"
)

func TestOwnedTCPListenerRequiresListenerProcessGroup(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	pgid, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !ownedTCPListener(port, pgid) {
		t.Fatal("listener was not attributed to its process group")
	}
	if ownedTCPListener(port, pgid+100000) {
		t.Fatal("listener was attributed to an unrelated process group")
	}
}
