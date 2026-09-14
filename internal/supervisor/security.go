package supervisor

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/git-treeline/cli/internal/platform"
)

const privateFileMode = 0600

// EnsureSocketDir creates the private directory that contains socketPath when
// it is absent. Existing directories are never repaired: a caller must not
// silently adopt a directory another user could have influenced.
func EnsureSocketDir(socketPath string) error {
	dir, err := socketDir(socketPath)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("creating supervisor socket directory: %w", err)
	}
	return validateSocketDirPath(dir)
}

// ValidateSocketDir verifies that socketPath is in a private, user-owned
// immediate child of /tmp. It never creates or changes filesystem state.
func ValidateSocketDir(socketPath string) error {
	dir, err := socketDir(socketPath)
	if err != nil {
		return err
	}
	return validateSocketDirPath(dir)
}

func socketDir(socketPath string) (string, error) {
	if filepath.Ext(socketPath) != ".sock" {
		return "", fmt.Errorf("invalid supervisor socket path %q", socketPath)
	}
	dir := filepath.Clean(filepath.Dir(socketPath))
	if filepath.Dir(dir) != "/tmp" {
		return "", fmt.Errorf("supervisor socket directory must be a direct child of /tmp: %s", dir)
	}
	return dir, nil
}

func validateSocketDirPath(dir string) error {
	// macOS exposes /tmp as a system-owned symlink to /private/tmp. Stat it so
	// that supported platform layout remains valid; the untrusted child itself
	// is always inspected with Lstat below.
	tmpInfo, err := os.Stat("/tmp")
	if err != nil {
		return fmt.Errorf("inspecting /tmp: %w", err)
	}
	if !tmpInfo.IsDir() || tmpInfo.Mode()&os.ModeSticky == 0 {
		return fmt.Errorf("/tmp is not a sticky directory")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspecting supervisor socket directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("supervisor socket directory is not a directory: %s", dir)
	}
	if err := validateOwnedPrivateDir(info); err != nil {
		return fmt.Errorf("unsafe supervisor socket directory %s: %w", dir, err)
	}
	return nil
}

func validateOwnedPrivateDir(info os.FileInfo) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a directory")
	}
	if ownerUID(info) != os.Geteuid() {
		return fmt.Errorf("owned by another user")
	}
	if info.Mode().Perm()&0077 != 0 || info.Mode().Perm()&0700 != 0700 {
		return fmt.Errorf("permissions are not private")
	}
	return nil
}

func validateOwnedPrivateRegular(info os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a regular file")
	}
	if ownerUID(info) != os.Geteuid() {
		return fmt.Errorf("owned by another user")
	}
	if info.Mode().Perm() != privateFileMode {
		return fmt.Errorf("permissions are not 0600")
	}
	return nil
}

func validateOwnedPrivateSocket(info os.FileInfo) error {
	if info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a socket")
	}
	if ownerUID(info) != os.Geteuid() {
		return fmt.Errorf("owned by another user")
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("permissions are not private")
	}
	return nil
}

func ownerUID(info os.FileInfo) int {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid)
	}
	return -1
}

// ReadStateFile reads an existing private supervisor state file without
// following a symlink.
func ReadStateFile(path string) ([]byte, error) {
	if err := ValidateSocketDir(stateSocketPath(path)); err != nil {
		return nil, err
	}
	// O_NONBLOCK prevents a malicious FIFO from hanging the caller before the
	// post-open fstat rejects it as a non-regular state file.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := validateOwnedPrivateRegular(info); err != nil {
		return nil, fmt.Errorf("unsafe supervisor state file %s: %w", path, err)
	}
	return io.ReadAll(f)
}

// WriteStateFile atomically replaces a private supervisor state file. A
// pre-existing symlink or insecure file is rejected instead of being repaired.
func WriteStateFile(path string, data []byte) error {
	if err := ValidateSocketDir(stateSocketPath(path)); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if err := validateOwnedPrivateRegular(info); err != nil {
			return fmt.Errorf("unsafe supervisor state file %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	return platform.AtomicWriteFile(path, data, privateFileMode)
}

// OpenStateLock opens a private lifetime-lock file without following a
// symlink. Callers own closing the returned file and any flock they take.
func OpenStateLock(path string) (*os.File, error) {
	if err := ValidateSocketDir(stateSocketPath(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, privateFileMode)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := validateOwnedPrivateRegular(info); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("unsafe supervisor state file %s: %w", path, err)
	}
	return f, nil
}

func stateSocketPath(path string) string {
	dir := filepath.Dir(path)
	return filepath.Join(dir, "state.sock")
}

func validateSocketEndpoint(socketPath string) error {
	if err := ValidateSocketDir(socketPath); err != nil {
		return err
	}
	info, err := os.Lstat(socketPath)
	if err != nil {
		return err
	}
	if err := validateOwnedPrivateSocket(info); err != nil {
		return fmt.Errorf("unsafe supervisor socket %s: %w", socketPath, err)
	}
	return nil
}

func validateLegacySocket(socketPath string) error {
	_, err := legacySocketInfo(socketPath)
	return err
}

func legacySocketInfo(socketPath string) (os.FileInfo, error) {
	info, err := os.Lstat(socketPath)
	if err != nil {
		return nil, err
	}
	if err := validateOwnedPrivateSocket(info); err != nil {
		return nil, fmt.Errorf("unsafe legacy supervisor socket %s: %w", socketPath, err)
	}
	return info, nil
}

func sendRawWithTimeout(socketPath, command string, timeout time.Duration) (string, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return "", fmt.Errorf("server not running (no socket at %s): %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte(command)); err != nil {
		return "", fmt.Errorf("sending command: %w", err)
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	resp, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	return string(resp), nil
}
