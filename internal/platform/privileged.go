package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PrivilegedFile is an in-memory snapshot to stage after privilege elevation.
type PrivilegedFile struct {
	Name string
	Data []byte
}

// ShellQuote encodes one literal POSIX shell word.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// StagePrivilegedFiles binds privileged consumers to captured bytes, without
// reopening a caller-writable pathname after authorization. Body may refer to
// the private directory as "$stage"; file names must be single safe components.
func StagePrivilegedFiles(files []PrivilegedFile, body string) (string, error) {
	var script strings.Builder
	script.WriteString("set -e\nPATH=/usr/sbin:/usr/bin:/sbin:/bin\nexport PATH\numask 077\n")
	script.WriteString("stage=$(/usr/bin/mktemp -d /tmp/git-treeline.XXXXXXXXXX)\n")
	script.WriteString("trap '/bin/rm -rf \"$stage\"' EXIT\ntrap 'exit 1' HUP INT TERM\n")
	seen := make(map[string]bool)
	for _, file := range files {
		if file.Name == "" || file.Name == "." || file.Name == ".." || seen[file.Name] || strings.ContainsAny(file.Name, "/\\\x00\r\n") {
			return "", fmt.Errorf("invalid privileged staging name %q", file.Name)
		}
		if strings.ContainsRune(string(file.Data), 0) {
			return "", fmt.Errorf("privileged staging file %q contains a NUL byte", file.Name)
		}
		seen[file.Name] = true
		fmt.Fprintf(&script, "printf '%%s' %s > \"$stage\"/%s\n", ShellQuote(string(file.Data)), ShellQuote(file.Name))
	}
	script.WriteString("cd \"$stage\"\n")
	script.WriteString(body)
	script.WriteByte('\n')
	return script.String(), nil
}

// RunPrivilegedScript streams a captured script to a fixed system shell. Sudo
// reads its password from the terminal, leaving stdin for the script bytes.
func RunPrivilegedScript(prompt, script string) error {
	cmd := exec.Command("/usr/bin/sudo", "-p", prompt, "/bin/sh", "-s")
	cmd.Stdin = strings.NewReader("set -e\nPATH=/usr/sbin:/usr/bin:/sbin:/bin\nexport PATH\numask 077\n" + script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
