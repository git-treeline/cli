package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func strictHostsFixture(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("GTL_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "hosts")
	content := "127.0.0.1 localhost\n# unrelated\n" + buildHostsBlock([]string{"retired.localhost", "survivor.localhost"}) + "\n::1 localhost\n"
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
	return path, content
}

func TestStrictManagedHostsPreservesFileAndUnrelatedContent(t *testing.T) {
	path, before := strictHostsFixture(t)
	snapshot, err := ReadManagedHostsStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	names := snapshot.Hostnames()
	names[0] = "mutated.localhost"
	if err := SyncManagedHostsNoninteractive(snapshot, []string{"survivor.localhost"}); err != nil {
		t.Fatal(err)
	}
	afterInfo, _ := os.Stat(path)
	after, _ := os.ReadFile(path)
	expected := strings.Replace(before, "127.0.0.1 retired.localhost\n", "", 1)
	if string(after) != expected || !os.SameFile(info, afterInfo) || info.Mode() != afterInfo.Mode() {
		t.Fatalf("file metadata/content changed: %s", after)
	}
	oldStat, newStat := info.Sys().(*syscall.Stat_t), afterInfo.Sys().(*syscall.Stat_t)
	if oldStat.Uid != newStat.Uid || oldStat.Gid != newStat.Gid {
		t.Fatal("ownership changed")
	}
	snapshot, err = ReadManagedHostsStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SyncManagedHostsNoninteractive(snapshot, nil); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(path)
	if string(after) != "127.0.0.1 localhost\n# unrelated\n::1 localhost\n" {
		t.Fatalf("unrelated content removed: %q", after)
	}
}

func TestStrictManagedHostsReadRefusesMalformedEvidence(t *testing.T) {
	for _, kind := range []string{"missing end", "duplicate", "stray end", "invalid host", "wrong address", "nested", "capacity", "NUL", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path, original := strictHostsFixture(t)
			content := original
			switch kind {
			case "missing end":
				content = hostsBegin() + "\n127.0.0.1 a.localhost\n"
			case "duplicate":
				content += buildHostsBlock([]string{"other.localhost"})
			case "stray end":
				content = hostsEnd() + "\n"
			case "invalid host":
				content = buildHostsBlock([]string{"bad\talias"})
			case "wrong address":
				content = strings.Replace(original, "127.0.0.1 retired", "192.0.2.1 retired", 1)
			case "nested":
				content = hostsBegin() + "\n# BEGIN git-treeline\n" + hostsEnd() + "\n"
			case "capacity":
				content = strings.Repeat("x", maxManagedHostsBytes+1)
			case "NUL":
				content = original + "\x00"
			case "directory":
				path = t.TempDir()
			case "symlink":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			}
			if kind != "directory" && kind != "symlink" {
				if err := os.WriteFile(path, []byte(content), 0640); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadManagedHostsStrict(path); err == nil {
				t.Fatal("malformed/unsafe file accepted")
			}
		})
	}
	path, _ := strictHostsFixture(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	absent, err := ReadManagedHostsStrict(path)
	if err != nil || len(absent.Hostnames()) != 0 {
		t.Fatalf("missing file not recognized: %v", err)
	}
	if err := SyncManagedHostsNoninteractive(absent, []string{"new.localhost"}); !errors.Is(err, ErrHostsChanged) {
		t.Fatal("writer created absent hosts", err)
	}
}

func TestStrictManagedHostsChangedOrBusyNeverWrites(t *testing.T) {
	for _, kind := range []string{"changed bytes", "replaced inode", "symlink replacement", "busy", "permission", "invalid desired"} {
		t.Run(kind, func(t *testing.T) {
			path, original := strictHostsFixture(t)
			snapshot, err := ReadManagedHostsStrict(path)
			if err != nil {
				t.Fatal(err)
			}
			want := ErrHostsChanged
			desired := []string{"survivor.localhost"}
			switch kind {
			case "changed bytes":
				if err := os.WriteFile(path, []byte(original+"# changed\n"), 0640); err != nil {
					t.Fatal(err)
				}
			case "replaced inode", "symlink replacement":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if kind == "replaced inode" {
					err = os.WriteFile(path, []byte(original), 0640)
				} else {
					err = os.Symlink(path+".old", path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "busy":
				f, err := os.OpenFile(path, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
				defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				want = ErrHostsBusy
			case "permission":
				if err := os.Chmod(path, 0400); err != nil {
					t.Fatal(err)
				}
				want = ErrHostsPrivilegeRequired
			case "invalid desired":
				desired = []string{"new.localhost\n192.0.2.1 injected"}
				want = nil
			}
			before, _ := os.ReadFile(path)
			err = SyncManagedHostsNoninteractive(snapshot, desired)
			if kind == "permission" && os.Geteuid() == 0 {
				// A privileged test process is already OS-authorized. Permission
				// refusal remains covered by the adapter's explicit fake outcome.
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || want != nil && !errors.Is(err, want) {
				t.Fatalf("wrong refusal: %v", err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("refusal modified file")
			}
		})
	}
}

func TestStrictManagedHostsConcurrentWriters(t *testing.T) {
	path, original := strictHostsFixture(t)
	snapshot, err := ReadManagedHostsStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, host := range []string{"first.localhost", "second.localhost"} {
		workers.Add(1)
		go func(host string) {
			defer workers.Done()
			<-start
			results <- SyncManagedHostsNoninteractive(snapshot, []string{host})
		}(host)
	}
	close(start)
	workers.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrHostsChanged) && !errors.Is(err, ErrHostsBusy) {
			t.Fatal(err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent stale writers succeeded: %d", succeeded)
	}
	after, err := ReadManagedHostsStrict(path)
	if err != nil || len(after.Hostnames()) != 1 {
		t.Fatalf("invalid readback: %v", err)
	}
	content, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(content), strings.Split(original, hostsBegin())[0]) || !strings.HasSuffix(string(content), "::1 localhost\n") {
		t.Fatal("concurrent write damaged unrelated content")
	}
}

func TestStrictManagedHostsReadWhileWriterLocked(t *testing.T) {
	path, _ := strictHostsFixture(t)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if _, err := ReadManagedHostsStrict(path); !errors.Is(err, ErrHostsBusy) {
		t.Fatalf("reader could observe partial strict write: %v", err)
	}
}

func TestBuildHostsBlock(t *testing.T) {
	block := buildHostsBlock([]string{"salt-main.localhost", "salt-feature.localhost"})
	if !strings.Contains(block, hostsBegin()) {
		t.Error("expected BEGIN marker")
	}
	if !strings.Contains(block, hostsEnd()) {
		t.Error("expected END marker")
	}
	if !strings.Contains(block, "127.0.0.1 salt-main.localhost") {
		t.Error("expected salt-main entry")
	}
	if !strings.Contains(block, "127.0.0.1 salt-feature.localhost") {
		t.Error("expected salt-feature entry")
	}
}

func TestReplaceHostsBlock_Insert(t *testing.T) {
	original := "127.0.0.1 localhost\n::1 localhost\n"
	block := buildHostsBlock([]string{"myapp.localhost"})
	result, err := replaceHostsBlock(original, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "127.0.0.1 localhost") {
		t.Error("original entries should be preserved")
	}
	if !strings.Contains(result, "127.0.0.1 myapp.localhost") {
		t.Error("new entry should be appended")
	}
}

func TestReplaceHostsBlock_Update(t *testing.T) {
	original := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 old-route.localhost\n" +
		hostsEnd() + "\n"
	block := buildHostsBlock([]string{"new-route.localhost"})
	result, err := replaceHostsBlock(original, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, "old-route") {
		t.Error("old entry should be removed")
	}
	if !strings.Contains(result, "new-route") {
		t.Error("new entry should be present")
	}
	if strings.Count(result, hostsBegin()) != 1 {
		t.Error("should have exactly one BEGIN marker")
	}
}

func TestReplaceHostsBlock_Remove(t *testing.T) {
	original := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 myapp.localhost\n" +
		hostsEnd() + "\n"
	result, err := replaceHostsBlock(original, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, hostsBegin()) {
		t.Error("managed block should be removed")
	}
	if !strings.Contains(result, "127.0.0.1 localhost") {
		t.Error("original entries should be preserved")
	}
}

func TestReplaceHostsBlock_MalformedMarkers(t *testing.T) {
	content := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 myapp.localhost\n"
	// No END marker — should return error, not truncate

	_, err := replaceHostsBlock(content, buildHostsBlock([]string{"new.localhost"}))
	if err == nil {
		t.Fatal("expected error for BEGIN without END marker")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("expected 'malformed' in error, got: %s", err)
	}

	// Same for removal
	_, err = replaceHostsBlock(content, "")
	if err == nil {
		t.Fatal("expected error for BEGIN without END on removal")
	}
}

func TestReplaceHostsBlock_EmptyBlock(t *testing.T) {
	content := "127.0.0.1 localhost\n"
	result, err := replaceHostsBlock(content, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != content {
		t.Errorf("removing non-existent block should be a no-op, got: %q", result)
	}
}

func TestReplaceHostsBlock_DuplicateMarkers(t *testing.T) {
	content := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 first.localhost\n" +
		hostsEnd() + "\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 second.localhost\n" +
		hostsEnd() + "\n"
	block := buildHostsBlock([]string{"new.localhost"})
	result, err := replaceHostsBlock(content, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "new.localhost") {
		t.Error("new entry should be present")
	}
	// First block is replaced; second remains
	if !strings.Contains(result, "second.localhost") {
		t.Error("second managed block should be preserved (only first is replaced)")
	}
}

func TestParseManagedHosts(t *testing.T) {
	content := "127.0.0.1 localhost\n" +
		hostsBegin() + "\n" +
		"127.0.0.1 salt-main.localhost\n" +
		"127.0.0.1 api-feature.localhost\n" +
		hostsEnd() + "\n"

	hosts := parseManagedHosts(content)
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d: %v", len(hosts), hosts)
	}
	if hosts[0] != "salt-main.localhost" || hosts[1] != "api-feature.localhost" {
		t.Errorf("unexpected hosts: %v", hosts)
	}
}

func TestParseManagedHosts_NoBlock(t *testing.T) {
	hosts := parseManagedHosts("127.0.0.1 localhost\n")
	if len(hosts) != 0 {
		t.Errorf("expected empty, got %v", hosts)
	}
}

func TestMissingHosts(t *testing.T) {
	// MissingHosts reads /etc/hosts which we can't control in unit tests.
	// Verify the logic: when managed is empty, all expected are missing.
	expected := []string{"a.localhost", "b.localhost"}
	missing := MissingHosts(expected)
	// On any system, either managed contains our entries or it doesn't.
	// At minimum, the function should not panic and should return a subset of expected.
	for _, h := range missing {
		found := false
		for _, e := range expected {
			if h == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("MissingHosts returned %q which is not in expected set", h)
		}
	}
}

func TestHostsNamesRejectRecordInjection(t *testing.T) {
	for _, name := range []string{"app.localhost\n203.0.113.1 victim.example", "app.localhost\r#", "app.localhost other.example", "app\t.localhost", "app#comment", "-app.localhost", "app..localhost"} {
		if err := validateHostsNames([]string{name}); err == nil {
			t.Errorf("accepted invalid hosts record %q", name)
		}
	}
	if err := validateHostsNames([]string{"main.app.localhost", "feature-12.app.prt.dev"}); err != nil {
		t.Fatal(err)
	}
}

func TestStrictManagedHostsWritePreflightDoesNotChangeFile(t *testing.T) {
	path, _ := strictHostsFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadManagedHostsStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckManagedHostsWrite(snapshot); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("preflight wrote hosts", err)
	}
	if err := os.Rename(path, path+".prior"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckManagedHostsWrite(snapshot); !errors.Is(err, ErrHostsChanged) {
		t.Fatal("replacement accepted", err)
	}
}
