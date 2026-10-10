package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivilegedStagingPreservesCapturedBytes(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	data := []byte("literal '\" $(touch " + marker + ") `touch " + marker + "`\nsecond line\n")
	want := string(data)
	script, err := StagePrivilegedFiles([]PrivilegedFile{{Name: "input", Data: data}},
		"printf '%s\\n' \"$stage\"; /bin/cat input")
	if err != nil {
		t.Fatal(err)
	}
	// A later change to the caller's source must not change the staged bytes.
	copy(data, []byte("replaced"))
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "untrusted'$(false)"))
	cmd := exec.Command("/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("staging: %v: %s", err, out)
	}
	stage, got, ok := strings.Cut(string(out), "\n")
	if !ok || got != want {
		t.Fatalf("staged bytes changed: %q", got)
	}
	if !strings.HasPrefix(stage, "/tmp/git-treeline.") {
		t.Fatalf("untrusted staging location: %q", stage)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("staging directory was not removed: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("file content executed as a command: %v", err)
	}
}

func TestPrivilegedStagingPrivateAndRemovedOnFailure(t *testing.T) {
	output := filepath.Join(t.TempDir(), "stage")
	script, err := StagePrivilegedFiles([]PrivilegedFile{{Name: "input", Data: []byte("data")}},
		"printf '%s' \"$stage\" > "+ShellQuote(output)+"\n[ \"$(umask)\" = 0077 ] || exit 9\nexit 7")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(script)
	err = cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
		t.Fatalf("expected body failure 7, got %v", err)
	}
	stage, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(stage)); !os.IsNotExist(err) {
		t.Fatalf("failed body left staging directory: %v", err)
	}
}

func TestPrivilegedStagingRejectsUnsafeInputs(t *testing.T) {
	for _, files := range [][]PrivilegedFile{
		{{Name: "../escape"}}, {{Name: "/absolute"}}, {{Name: "a/b"}}, {{Name: ".."}},
		{{Name: "same"}, {Name: "same"}}, {{Name: "file", Data: []byte{'a', 0, 'b'}}},
	} {
		if _, err := StagePrivilegedFiles(files, "true"); err == nil {
			t.Errorf("accepted unsafe staging input: %+v", files)
		}
	}
}
