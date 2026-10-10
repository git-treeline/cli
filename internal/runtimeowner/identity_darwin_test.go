//go:build darwin

package runtimeowner

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestDecodeDarwinKernelIdentity(t *testing.T) {
	var info unix.KinfoProc
	info.Proc.P_pid, info.Proc.P_stat = 10, 3
	info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec = 1000, 25
	info.Eproc.Ucred.Uid = 501
	info.Eproc.Pcred.P_ruid, info.Eproc.Pcred.P_svuid = 501, 501
	if p, err := decodeDarwinProcess(&info, 10); err != nil || p != (processIdentity{PID: 10, UID: 501, BirthID: "1000:25"}) {
		t.Fatalf("decoded %+v: %v", p, err)
	}
	for name, mutate := range map[string]func(*unix.KinfoProc){
		"wrong PID":       func(v *unix.KinfoProc) { v.Proc.P_pid++ },
		"zombie":          func(v *unix.KinfoProc) { v.Proc.P_stat = 5 },
		"forking":         func(v *unix.KinfoProc) { v.Proc.P_stat = 1 },
		"missing birth":   func(v *unix.KinfoProc) { v.Proc.P_starttime.Sec = 0 },
		"negative micros": func(v *unix.KinfoProc) { v.Proc.P_starttime.Usec = -1 },
		"oversize micros": func(v *unix.KinfoProc) { v.Proc.P_starttime.Usec = 1000000 },
		"real UID":        func(v *unix.KinfoProc) { v.Eproc.Pcred.P_ruid++ },
		"saved UID":       func(v *unix.KinfoProc) { v.Eproc.Pcred.P_svuid++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := info
			mutate(&changed)
			if _, err := decodeDarwinProcess(&changed, 10); err == nil {
				t.Fatal("invalid kernel evidence accepted")
			}
		})
	}
	if _, err := decodeDarwinProcess(nil, 10); err == nil {
		t.Fatal("missing process accepted")
	}
}
