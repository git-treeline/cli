package format

import (
	"os"
	"path/filepath"
	"slices"
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
	if err := DropSingleDB(alloc, t.TempDir(), nil); err != nil {
		t.Errorf("expected nil error for empty database name, got %v", err)
	}
}

func TestDropSingleDB_SQLite(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "test.db")
	_ = os.WriteFile(dbFile, []byte("sqlite"), 0o644)

	alloc := Allocation{
		"database":         "test.db",
		"database_adapter": "sqlite",
	}
	if err := DropSingleDB(alloc, dir, nil); err != nil {
		t.Errorf("expected nil error on successful drop, got %v", err)
	}

	if _, err := os.Stat(dbFile); !os.IsNotExist(err) {
		t.Error("expected sqlite file to be removed")
	}
}

func TestDropSingleDB_UnknownAdapter(t *testing.T) {
	alloc := Allocation{
		"database":         "mydb",
		"database_adapter": "nonexistent_adapter",
	}
	if err := DropSingleDB(alloc, t.TempDir(), nil); err == nil {
		t.Error("expected error for unknown adapter, got nil")
	}
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
	// The unknown-adapter entry fails, so a non-nil error is expected even
	// though the sqlite drop succeeds.
	if err := DropDatabases(allocs, nil); err == nil {
		t.Error("expected error naming the failed drop, got nil")
	}

	if _, err := os.Stat(dbFile); !os.IsNotExist(err) {
		t.Error("expected sqlite file to be removed by DropDatabases")
	}
}

// fakeListerAdapter implements database.Adapter plus ListDatabases so shard
// expansion can be tested without a server.
type fakeListerAdapter struct {
	databases []string
	listErr   error
	dropped   []string
}

func (f *fakeListerAdapter) Clone(template, target string) error { return nil }
func (f *fakeListerAdapter) Drop(target string) error {
	f.dropped = append(f.dropped, target)
	return nil
}
func (f *fakeListerAdapter) Exists(name string) (bool, error)      { return false, nil }
func (f *fakeListerAdapter) Rename(oldName, newName string) error  { return nil }
func (f *fakeListerAdapter) Restore(target, dumpFile string) error { return nil }
func (f *fakeListerAdapter) ListDatabases() ([]string, error)      { return f.databases, f.listErr }

func TestShardTargets_MatchesShardsAndHonorsKeep(t *testing.T) {
	serverDBs := []string{
		"salt_x_test", "salt_x_test_0", "salt_x_test_1",
		"salt_x_test_extra", // suffix is not a pure number — must not match
		"salt_x_testing",    // prefix-only — must not match
		"salt_y_test",
	}
	got := shardTargets("salt_x_test", true, serverDBs, true, map[string]bool{"salt_x_test_1": true})
	want := []string{"salt_x_test", "salt_x_test_0"}
	if !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestShardTargets_NoListingFallsBackToExactName(t *testing.T) {
	got := shardTargets("salt_x_test", true, nil, false, nil)
	if !slices.Equal(got, []string{"salt_x_test"}) {
		t.Errorf("expected exact-name fallback, got %v", got)
	}
}

func TestShardTargets_PrimaryIsNeverExpanded(t *testing.T) {
	serverDBs := []string{"salt_dev", "salt_dev_2"}
	got := shardTargets("salt_dev", false, serverDBs, true, nil)
	if !slices.Equal(got, []string{"salt_dev"}) {
		t.Errorf("expected exact primary drop only, got %v", got)
	}
}

func TestShardTargets_KeepBlocksExactName(t *testing.T) {
	if got := shardTargets("salt_x_test", true, []string{"salt_x_test"}, true, map[string]bool{"salt_x_test": true}); len(got) != 0 {
		t.Errorf("expected kept database to be excluded, got %v", got)
	}
	if got := shardTargets("salt_x", false, nil, false, map[string]bool{"salt_x": true}); len(got) != 0 {
		t.Errorf("expected kept primary to be excluded, got %v", got)
	}
}

func TestListDatabasesOnce(t *testing.T) {
	lister := &fakeListerAdapter{databases: []string{"a", "b"}}
	if _, ok := listDatabasesOnce(lister, []string{"only_primary"}); ok {
		t.Error("expected no listing when there are no auxiliary entries")
	}
	if got, ok := listDatabasesOnce(lister, []string{"dev", "test"}); !ok || !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("expected server list, got %v (ok=%v)", got, ok)
	}
	failing := &fakeListerAdapter{listErr: os.ErrPermission}
	if _, ok := listDatabasesOnce(failing, []string{"dev", "test"}); ok {
		t.Error("expected failed listing to report ok=false")
	}
}

func TestDropDatabases_SQLiteList(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "dev.db")
	auxiliary := filepath.Join(dir, "test.db")
	_ = os.WriteFile(primary, []byte("data"), 0o644)
	_ = os.WriteFile(auxiliary, []byte("data"), 0o644)

	allocs := []Allocation{{
		"database":         "dev.db",
		"databases":        []any{"dev.db", "test.db"},
		"database_adapter": "sqlite",
		"worktree":         dir,
	}}
	if err := DropDatabases(allocs, nil); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if _, err := os.Stat(primary); !os.IsNotExist(err) {
		t.Error("expected primary sqlite file to be removed")
	}
	if _, err := os.Stat(auxiliary); !os.IsNotExist(err) {
		t.Error("expected auxiliary sqlite file to be removed")
	}
}

func TestDropDatabases_KeepGuardsPrimary(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "dev.db")
	aux := filepath.Join(dir, "test.db")
	_ = os.WriteFile(primary, []byte("data"), 0o644)
	_ = os.WriteFile(aux, []byte("data"), 0o644)

	allocs := []Allocation{{
		"databases":        []any{"dev.db", "test.db"},
		"database_adapter": "sqlite",
		"worktree":         dir,
	}}
	if err := DropDatabases(allocs, map[string]bool{"dev.db": true}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if _, err := os.Stat(primary); err != nil {
		t.Error("expected kept primary to survive the drop")
	}
	if _, err := os.Stat(aux); !os.IsNotExist(err) {
		t.Error("expected auxiliary database to be dropped")
	}
}
