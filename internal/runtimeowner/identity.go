package runtimeowner

import (
	"errors"
	"strconv"
	"strings"
)

var errOwnerEvidence = errors.New("exact runtime owner evidence unavailable")

type processIdentity struct {
	PID     int
	UID     uint32
	BirthID string
}

// ObserveOwner observes only the requested process, twice, bracketed by kernel
// boot identity reads. It refuses missing, replaced, foreign, or dead owners.
func ObserveOwner(pid int) (Owner, error) { return observePlatformOwner(pid) }

// VerifyOwner never falls back to PID-only evidence. The expected identity must
// be an exact retained kernel boot/PID/birth tuple from this user.
func VerifyOwner(expected Owner) bool {
	if !validOwner(expected) {
		return false
	}
	actual, err := ObserveOwner(expected.PID)
	return err == nil && actual == expected
}

func observeOwner(pid int, uid uint32, boot func() (string, error), process func(int) (processIdentity, error)) (Owner, error) {
	if pid <= 0 || pid > 2147483647 {
		return Owner{}, errOwnerEvidence
	}
	firstBoot, err := boot()
	if err != nil || !validUUID(firstBoot) {
		return Owner{}, errOwnerEvidence
	}
	first, err := process(pid)
	if err != nil || first.PID != pid || first.UID != uid {
		return Owner{}, errOwnerEvidence
	}
	middleBoot, err := boot()
	if err != nil || middleBoot != firstBoot {
		return Owner{}, errOwnerEvidence
	}
	second, err := process(pid)
	if err != nil || first != second {
		return Owner{}, errOwnerEvidence
	}
	lastBoot, err := boot()
	owner := Owner{BootID: firstBoot, PID: pid, BirthID: first.BirthID}
	if err != nil || lastBoot != firstBoot || !validOwner(owner) {
		return Owner{}, errOwnerEvidence
	}
	return owner, nil
}

func decodeLinuxStat(raw []byte, pid int) (string, error) {
	if len(raw) == 0 || len(raw) > 65536 {
		return "", errOwnerEvidence
	}
	text := string(raw)
	open, close := strings.Index(text, " ("), strings.LastIndex(text, ") ")
	if open < 1 || close <= open || text[:open] != strconv.Itoa(pid) {
		return "", errOwnerEvidence
	}
	fields := strings.Fields(text[close+2:])
	// State is field 3; the kernel's start-time tick count is field 22.
	if len(fields) < 20 || !decimal(fields[19], true) {
		return "", errOwnerEvidence
	}
	switch fields[0] {
	case "R", "S", "D", "T", "t", "I", "P":
		return fields[19], nil
	default:
		return "", errOwnerEvidence
	}
}

func decodeLinuxUID(raw []byte) (uint32, error) {
	if len(raw) == 0 || len(raw) > 65536 {
		return 0, errOwnerEvidence
	}
	var uid uint64
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		if found {
			return 0, errOwnerEvidence
		}
		fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
		if len(fields) != 4 {
			return 0, errOwnerEvidence
		}
		for i, field := range fields {
			parsed, err := strconv.ParseUint(field, 10, 32)
			if err != nil || strconv.FormatUint(parsed, 10) != field || i > 0 && parsed != uid {
				return 0, errOwnerEvidence
			}
			uid = parsed
		}
		found = true
	}
	if !found {
		return 0, errOwnerEvidence
	}
	return uint32(uid), nil
}
