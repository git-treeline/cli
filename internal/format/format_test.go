package format

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJoinInts(t *testing.T) {
	tests := []struct {
		ints []int
		sep  string
		want string
	}{
		{[]int{1, 2, 3}, ", ", "1, 2, 3"},
		{[]int{3000}, ",", "3000"},
		{[]int{}, ", ", ""},
		{nil, ", ", ""},
	}
	for _, tt := range tests {
		got := JoinInts(tt.ints, tt.sep)
		if got != tt.want {
			t.Errorf("JoinInts(%v, %q) = %q, want %q", tt.ints, tt.sep, got, tt.want)
		}
	}
}

func TestGetPorts_FromPortsArray(t *testing.T) {
	a := Allocation{
		"ports": []any{float64(3000), float64(3001)},
	}
	ports := GetPorts(a)
	if len(ports) != 2 || ports[0] != 3000 || ports[1] != 3001 {
		t.Errorf("GetPorts = %v, want [3000, 3001]", ports)
	}
}

func TestGetPorts_FromSinglePort(t *testing.T) {
	a := Allocation{
		"port": float64(3010),
	}
	ports := GetPorts(a)
	if len(ports) != 1 || ports[0] != 3010 {
		t.Errorf("GetPorts = %v, want [3010]", ports)
	}
}

func TestGetPorts_Empty(t *testing.T) {
	a := Allocation{}
	ports := GetPorts(a)
	if ports != nil {
		t.Errorf("GetPorts = %v, want nil", ports)
	}
}

func TestGetStr(t *testing.T) {
	a := Allocation{
		"project":  "myapp",
		"database": "myapp_dev",
	}
	if got := GetStr(a, "project"); got != "myapp" {
		t.Errorf("GetStr(project) = %q, want %q", got, "myapp")
	}
	if got := GetStr(a, "missing"); got != "" {
		t.Errorf("GetStr(missing) = %q, want empty", got)
	}
}

func TestDisplayName_PrefersBranch(t *testing.T) {
	a := Allocation{"branch": "feature-auth", "worktree_name": "abc123"}
	if got := DisplayName(a); got != "feature-auth" {
		t.Errorf("DisplayName = %q, want %q", got, "feature-auth")
	}
}

func TestDisplayName_FallsBackToWorktreeName(t *testing.T) {
	a := Allocation{"worktree_name": "my-worktree"}
	if got := DisplayName(a); got != "my-worktree" {
		t.Errorf("DisplayName = %q, want %q", got, "my-worktree")
	}
}

func TestDisplayName_EmptyBranchFallsBack(t *testing.T) {
	a := Allocation{"branch": "", "worktree_name": "dir-name"}
	if got := DisplayName(a); got != "dir-name" {
		t.Errorf("DisplayName = %q, want %q", got, "dir-name")
	}
}

func TestPortDisplay(t *testing.T) {
	tests := []struct {
		name string
		a    Allocation
		want string
	}{
		{"with ports", Allocation{"ports": []any{float64(3000)}}, ":3000"},
		{"with port", Allocation{"port": float64(3010)}, ":3010"},
		{"empty", Allocation{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PortDisplay(tt.a); got != tt.want {
				t.Errorf("PortDisplay = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDropSingleDB_EmptyName(t *testing.T) {
	alloc := Allocation{"database": "", "database_adapter": "sqlite"}
	DropSingleDB(alloc, t.TempDir())
}

func TestDropSingleDB_SQLite(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "test.db")
	_ = os.WriteFile(dbFile, []byte("sqlite"), 0o644)

	alloc := Allocation{
		"database":         "test.db",
		"database_adapter": "sqlite",
	}
	DropSingleDB(alloc, dir)

	if _, err := os.Stat(dbFile); !os.IsNotExist(err) {
		t.Error("expected sqlite file to be removed")
	}
}

func TestDropSingleDB_UnknownAdapter(t *testing.T) {
	alloc := Allocation{
		"database":         "mydb",
		"database_adapter": "nonexistent_adapter",
	}
	DropSingleDB(alloc, t.TempDir())
}

func TestDropDatabases_MixedEntries(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "drop_me.db")
	_ = os.WriteFile(dbFile, []byte("data"), 0o644)

	allocs := []Allocation{
		{"database": "", "database_adapter": "sqlite"},
		{"database": "drop_me.db", "database_adapter": "sqlite", "worktree": dir},
		{"database": "x", "database_adapter": "nonexistent"},
	}
	DropDatabases(allocs)

	if _, err := os.Stat(dbFile); !os.IsNotExist(err) {
		t.Error("expected sqlite file to be removed by DropDatabases")
	}
}
