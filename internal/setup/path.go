package setup

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func rootedName(relative, field string) (string, error) {
	if !filepath.IsLocal(relative) || hasParentPathElement(relative) {
		return "", fmt.Errorf("unsafe %s %q: path must be relative and remain within its project root", field, relative)
	}
	return filepath.Clean(relative), nil
}

func openRoot(path, field string) (*os.Root, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolving root for %s: %w", field, err)
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, fmt.Errorf("opening root for %s: %w", field, err)
	}
	return root, nil
}

// OpenFileForRead opens a project-relative file without permitting any
// symlink component to escape root. The returned file remains valid after the
// temporary root handle is closed.
func OpenFileForRead(rootPath, relative, field string) (*os.File, error) {
	root, err := openRoot(rootPath, field)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	name, err := containedRootName(root, relative, field, true)
	if err != nil {
		return nil, err
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("opening %s %q within project root: %w", field, relative, err)
	}
	return file, nil
}

func containedRootName(root *os.Root, relative, field string, followFinal bool) (string, error) {
	if _, err := rootedName(relative, field); err != nil {
		return "", err
	}
	absolute, err := resolveContainedPath(root.Name(), relative, followFinal)
	if err != nil {
		return "", fmt.Errorf("unsafe %s %q: %w", field, relative, err)
	}
	name, err := filepath.Rel(root.Name(), absolute)
	if err != nil || (name != "." && !filepath.IsLocal(name)) {
		return "", fmt.Errorf("unsafe %s %q: path resolves outside its project root", field, relative)
	}
	return name, nil
}

func resolveContainedPath(root, relative string, followFinal bool) (string, error) {
	walk := filepath.Join(root, filepath.Clean(relative))
	leaf := ""
	if !followFinal {
		leaf = filepath.Base(walk)
		walk = filepath.Dir(walk)
	}
	resolved, suffix, err := resolveExistingAncestor(walk)
	if err != nil {
		return "", err
	}
	if !isWithinRoot(root, resolved) {
		return "", fmt.Errorf("symlink resolves outside %s", root)
	}
	result := filepath.Join(append([]string{resolved}, suffix...)...)
	if leaf != "" {
		result = filepath.Join(result, leaf)
	}
	return result, nil
}

func resolveExistingAncestor(path string) (string, []string, error) {
	current := path
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return resolved, suffix, nil
		}
		info, lstatErr := os.Lstat(current)
		if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("cannot safely resolve symlink %s", current)
		}
		if lstatErr != nil && !os.IsNotExist(lstatErr) {
			return "", nil, lstatErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil, err
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}

func isWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func atomicWriteRoot(root *os.Root, name string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(name)
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var tempName string
	var temp *os.File
	for range 100 {
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		tempName = filepath.Join(dir, ".tmp-"+hex.EncodeToString(random))
		var err error
		temp, err = root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_RDWR, perm)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	if temp == nil {
		return fmt.Errorf("creating temporary file for %s", name)
	}
	defer func() { _ = root.Remove(tempName) }()
	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return root.Rename(tempName, name)
}

func hasParentPathElement(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return true
		}
	}
	return false
}
