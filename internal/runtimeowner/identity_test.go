package runtimeowner

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestObserveOwnerRequiresRepeatedBootAndProcessEvidence(t *testing.T) {
	bootID := uuid.NewString()
	process := processIdentity{PID: 10, UID: 501, BirthID: "1000:25"}
	for _, scenario := range []string{"stable", "wrong PID", "foreign UID", "birth reused", "boot changed middle", "boot changed last", "boot error", "process error", "invalid birth", "invalid boot"} {
		t.Run(scenario, func(t *testing.T) {
			bootCalls, processCalls := 0, 0
			owner, err := observeOwner(10, 501, func() (string, error) {
				bootCalls++
				if scenario == "boot error" && bootCalls == 3 {
					return "", errors.New("fixture failure")
				}
				if scenario == "boot changed middle" && bootCalls == 2 || scenario == "boot changed last" && bootCalls == 3 {
					return uuid.NewString(), nil
				}
				if scenario == "invalid boot" {
					return "host-name", nil
				}
				return bootID, nil
			}, func(pid int) (processIdentity, error) {
				if pid != 10 {
					t.Fatalf("probed unrelated PID %d", pid)
				}
				processCalls++
				evidence := process
				switch scenario {
				case "wrong PID":
					evidence.PID++
				case "foreign UID":
					evidence.UID++
				case "birth reused":
					if processCalls == 2 {
						evidence.BirthID = "1000:26"
					}
				case "process error":
					if processCalls == 2 {
						return processIdentity{}, errors.New("fixture failure")
					}
				case "invalid birth":
					evidence.BirthID = "some command"
				}
				return evidence, nil
			})
			if scenario == "stable" {
				if err != nil || owner != (Owner{BootID: bootID, PID: 10, BirthID: "1000:25"}) || bootCalls != 3 || processCalls != 2 {
					t.Fatalf("incomplete observation: %+v %v calls=%d/%d", owner, err, bootCalls, processCalls)
				}
			} else if !errors.Is(err, errOwnerEvidence) || owner != (Owner{}) {
				t.Fatalf("ambiguous evidence accepted: %+v %v", owner, err)
			}
		})
	}
}

func TestObserveInvalidPIDDoesNotReadKernel(t *testing.T) {
	for _, pid := range []int{-1, 0, 2147483648} {
		_, err := observeOwner(pid, 501, func() (string, error) { t.Fatal("boot queried for invalid PID"); return "", nil }, func(int) (processIdentity, error) { t.Fatal("invalid PID queried"); return processIdentity{}, nil })
		if !errors.Is(err, errOwnerEvidence) {
			t.Fatal(err)
		}
	}
}

func linuxStatFixture(pid, command, state, birth string) []byte {
	return []byte(pid + " (" + command + ") " + state + " " + strings.Repeat("0 ", 18) + birth + " 0 0\n")
}

func TestDecodeLinuxStatHandlesCommandDelimitersWithoutPersistingThem(t *testing.T) {
	for _, command := range []string{"supervisor", "space in name", "nested (name) ) command", "line\nbreak"} {
		birth, err := decodeLinuxStat(linuxStatFixture("10", command, "S", "9876"), 10)
		if err != nil || birth != "9876" {
			t.Fatalf("decode: %q %v", birth, err)
		}
	}
	for _, raw := range [][]byte{
		linuxStatFixture("11", "wrong PID", "S", "9876"),
		linuxStatFixture("010", "noncanonical PID", "S", "9876"),
		linuxStatFixture("10", "zombie", "Z", "9876"),
		linuxStatFixture("10", "dead", "X", "9876"),
		linuxStatFixture("10", "bad birth", "S", "0"),
		linuxStatFixture("10", "bad birth", "S", "009876"),
		linuxStatFixture("10", "bad birth", "S", "-1"),
		[]byte("10 (truncated) S 1 2"),
		[]byte(strings.Repeat("x", 65537)),
	} {
		if _, err := decodeLinuxStat(raw, 10); !errors.Is(err, errOwnerEvidence) {
			t.Fatal("invalid stat accepted", err)
		}
	}
}

func TestDecodeLinuxUIDRequiresAllCredentials(t *testing.T) {
	if uid, err := decodeLinuxUID([]byte("Name:\tfixture\nUid:\t501\t501\t501\t501\n")); err != nil || uid != 501 {
		t.Fatalf("uid %d: %v", uid, err)
	}
	for _, raw := range []string{
		"", "Name:\tno-uid", "Uid: 501 0 501 501", "Uid: 0 501 501 501", "Uid: 501 501 0 501", "Uid: 501 501 501 0", "Uid: 501 501 501", "Uid: 0501 0501 0501 0501", "Uid: 501 501 501 501\nUid: 501 501 501 501", "Uid: secret secret secret secret",
	} {
		if _, err := decodeLinuxUID([]byte(raw)); !errors.Is(err, errOwnerEvidence) {
			t.Fatal("invalid credentials accepted", err)
		}
	}
}
