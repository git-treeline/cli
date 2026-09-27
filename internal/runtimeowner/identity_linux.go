//go:build linux

package runtimeowner

import (
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func observePlatformOwner(pid int) (Owner, error) {
	return observeOwner(pid, uint32(os.Geteuid()), func() (string, error) {
		raw, _, err := readProcFile("/proc/sys/kernel/random/boot_id")
		return strings.TrimSuffix(string(raw), "\n"), err
	}, func(pid int) (processIdentity, error) {
		base := "/proc/" + strconv.Itoa(pid)
		raw, fileUID, err := readProcFile(base + "/stat")
		if err != nil {
			return processIdentity{}, errOwnerEvidence
		}
		birth, err := decodeLinuxStat(raw, pid)
		if err != nil {
			return processIdentity{}, err
		}
		status, statusUID, err := readProcFile(base + "/status")
		if err != nil {
			return processIdentity{}, errOwnerEvidence
		}
		uid, err := decodeLinuxUID(status)
		if err != nil || fileUID != uid || statusUID != uid {
			return processIdentity{}, errOwnerEvidence
		}
		return processIdentity{PID: pid, UID: uid, BirthID: birth}, nil
	})
}

func readProcFile(path string) ([]byte, uint32, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, errOwnerEvidence
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, errOwnerEvidence
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, 0, errOwnerEvidence
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, 0, errOwnerEvidence
	}
	return raw, stat.Uid, nil
}
