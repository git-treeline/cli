//go:build darwin

package runtimeowner

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func observePlatformOwner(pid int) (Owner, error) {
	return observeOwner(pid, uint32(os.Geteuid()), func() (string, error) {
		boot, err := unix.Sysctl("kern.bootsessionuuid")
		return strings.ToLower(boot), err
	}, func(pid int) (processIdentity, error) {
		info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			return processIdentity{}, errOwnerEvidence
		}
		return decodeDarwinProcess(info, pid)
	})
}

func decodeDarwinProcess(info *unix.KinfoProc, pid int) (processIdentity, error) {
	if info == nil || int(info.Proc.P_pid) != pid || info.Proc.P_starttime.Sec <= 0 || info.Proc.P_starttime.Usec < 0 || info.Proc.P_starttime.Usec >= 1000000 || info.Eproc.Ucred.Uid != info.Eproc.Pcred.P_ruid || info.Eproc.Ucred.Uid != info.Eproc.Pcred.P_svuid {
		return processIdentity{}, errOwnerEvidence
	}
	// Darwin sys/proc.h: runnable=2, sleeping=3, stopped=4. Creation and
	// zombie records do not establish a ready, live supervisor identity.
	if info.Proc.P_stat < 2 || info.Proc.P_stat > 4 {
		return processIdentity{}, errOwnerEvidence
	}
	return processIdentity{PID: pid, UID: info.Eproc.Ucred.Uid, BirthID: fmt.Sprintf("%d:%d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec)}, nil
}
