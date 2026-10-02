//go:build !darwin

package supervisor

func ownedTCPListener(port, pgid int) bool { return false }
