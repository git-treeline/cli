package cmd

import (
	"os"
	"testing"
)

func TestIsInsideDir_Exact(t *testing.T) {
	if !isInsideDir("/a/b/c", "/a/b/c") {
		t.Error("expected true for exact match")
	}
}

func TestIsInsideDir_Child(t *testing.T) {
	if !isInsideDir("/a/b/c/d", "/a/b/c") {
		t.Error("expected true for child path")
	}
}

func TestIsInsideDir_Sibling(t *testing.T) {
	if isInsideDir("/a/b/cd", "/a/b/c") {
		t.Error("expected false for sibling with shared prefix")
	}
}

func TestIsInsideDir_Parent(t *testing.T) {
	if isInsideDir("/a/b", "/a/b/c") {
		t.Error("expected false when cwd is parent of dir")
	}
}

func TestIsInsideDir_Unrelated(t *testing.T) {
	if isInsideDir("/x/y", "/a/b") {
		t.Error("expected false for unrelated paths")
	}
}

func TestIsInsideDir_PlatformSeparator(t *testing.T) {
	sep := string(os.PathSeparator)
	dir := sep + "workspace" + sep + "project"
	cwd := dir + sep + "subdir"
	if !isInsideDir(cwd, dir) {
		t.Error("expected true for child using platform separator")
	}
}
