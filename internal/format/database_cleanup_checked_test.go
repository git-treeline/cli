package format

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
)

type checkedDropFake struct {
	database.Adapter
	drops []string
	drop  func(string) error
}

func (f *checkedDropFake) Drop(name string) error {
	f.drops = append(f.drops, name)
	if f.drop != nil {
		return f.drop(name)
	}
	return nil
}

type checkedListFake struct {
	*checkedDropFake
	list func() ([]string, error)
}

func (f *checkedListFake) ListDatabases() ([]string, error) { return f.list() }

func checkedCleanupFixture(t *testing.T, adapter string) (string, string, registry.CleanupSnapshot) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".treeline.yml"), []byte("project: fixture\ndatabase:\n  adapter: "+adapter+"\n  host: localhost\n  pattern: old_pattern\n"), 0600); err != nil {
		t.Fatal(err)
	}
	revision, err := DatabaseCleanupConfigRevision(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, revision, registry.CleanupSnapshot{DatabaseAdapter: adapter, Databases: []string{"app", "app_test"}}
}
func checkedOptions(revision string, adapter database.Adapter) DatabaseCleanupCheck {
	return DatabaseCleanupCheck{ConfigRevision: revision, BeforeDrop: func() error { return nil }, NewAdapter: func(string, []string) (database.Adapter, error) { return adapter, nil }}
}

func TestCheckedDatabaseCleanupSharedAndTemplateProtection(t *testing.T) {
	root, _, snapshot := checkedCleanupFixture(t, "postgresql")
	config := []byte("project: fixture\ndatabase:\n  adapter: postgresql\n  template: app\n  host: localhost\n  pattern: old_pattern\n")
	if err := os.WriteFile(filepath.Join(root, ".treeline.yml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	revision, err := DatabaseCleanupConfigRevision(root)
	if err != nil {
		t.Fatal(err)
	}
	drop := &checkedDropFake{}
	adapter := &checkedListFake{drop, func() ([]string, error) { return []string{"app", "app_test", "app_test_1", "unrelated"}, nil }}
	options := checkedOptions(revision, adapter)
	options.NewAdapter = func(name string, args []string) (database.Adapter, error) {
		if name != "postgresql" || !reflect.DeepEqual(args, []string{"-h", "localhost"}) {
			t.Fatalf("connection changed: %s %v", name, args)
		}
		return adapter, nil
	}
	attempted, err := DropDatabasesChecked(context.Background(), root, snapshot, map[string]bool{"app_test_1": true}, options)
	if err != nil || !attempted || !reflect.DeepEqual(drop.drops, []string{"app_test"}) {
		t.Fatalf("drop plan: %v %v %v", attempted, err, drop.drops)
	}
	after, err := os.ReadFile(filepath.Join(root, ".treeline.yml"))
	if err != nil || string(after) != string(config) {
		t.Fatal("checked read migrated config", err)
	}
}

func TestCheckedDatabaseCleanupRefusesIncompletePlan(t *testing.T) {
	for _, kind := range []string{"listing", "malformed config", "revision", "config changes during listing", "factory", "bad name", "missing guard", "cancelled", "invalid host type", "invalid template type", "missing config", "symlink config"} {
		t.Run(kind, func(t *testing.T) {
			root, revision, snapshot := checkedCleanupFixture(t, "postgresql")
			drop := &checkedDropFake{}
			adapter := &checkedListFake{drop, func() ([]string, error) { return []string{"app", "app_test"}, nil }}
			options := checkedOptions(revision, adapter)
			ctx := context.Background()
			switch kind {
			case "missing config":
				if err := os.Remove(filepath.Join(root, ".treeline.yml")); err != nil {
					t.Fatal(err)
				}
			case "symlink config":
				original := filepath.Join(root, ".treeline.yml")
				moved := filepath.Join(root, "config-original")
				if err := os.Rename(original, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, original); err != nil {
					t.Fatal(err)
				}
			case "listing":
				adapter.list = func() ([]string, error) { return nil, errors.New("private credentials") }
			case "invalid host type", "invalid template type":
				field := "host"
				if kind == "invalid template type" {
					field = "template"
				}
				_ = os.WriteFile(filepath.Join(root, ".treeline.yml"), []byte("database:\n  adapter: postgresql\n  "+field+": [bad]\n"), 0600)
				options.ConfigRevision, _ = DatabaseCleanupConfigRevision(root)
			case "malformed config":
				_ = os.WriteFile(filepath.Join(root, ".treeline.yml"), []byte("database: ["), 0600)
				options.ConfigRevision, _ = DatabaseCleanupConfigRevision(root)
			case "revision":
				options.ConfigRevision = strings.Repeat("0", 64)
			case "config changes during listing":
				adapter.list = func() ([]string, error) {
					_ = os.WriteFile(filepath.Join(root, ".treeline.yml"), []byte("project: changed\n"), 0600)
					return []string{"app", "app_test"}, nil
				}
			case "factory":
				options.NewAdapter = func(string, []string) (database.Adapter, error) { return nil, errors.New("private credentials") }
			case "bad name":
				snapshot.Databases = []string{"app", "--unsafe"}
			case "missing guard":
				options.BeforeDrop = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			attempted, err := DropDatabasesChecked(ctx, root, snapshot, nil, options)
			if err == nil || attempted || len(drop.drops) != 0 || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe plan: %v %v %v", attempted, err, drop.drops)
			}
		})
	}
}

func TestCheckedDatabaseCleanupPartialEffectIsUncertain(t *testing.T) {
	for _, kind := range []string{"drop failure", "config changed", "guard changed", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			root, revision, snapshot := checkedCleanupFixture(t, "postgresql")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			drop := &checkedDropFake{}
			adapter := &checkedListFake{drop, func() ([]string, error) { return []string{"app", "app_test"}, nil }}
			options := checkedOptions(revision, adapter)
			changed := false
			options.BeforeDrop = func() error {
				if changed {
					return errors.New("changed")
				}
				return nil
			}
			drop.drop = func(string) error {
				switch kind {
				case "drop failure":
					return errors.New("private credentials")
				case "config changed":
					return os.WriteFile(filepath.Join(root, ".treeline.yml"), []byte("project: changed\n"), 0600)
				case "guard changed":
					changed = true
				case "cancelled":
					cancel()
				}
				return nil
			}
			attempted, err := DropDatabasesChecked(ctx, root, snapshot, nil, options)
			if err == nil || !attempted || len(drop.drops) != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("partial effect: %v %v %v", attempted, err, drop.drops)
			}
		})
	}
}

func TestCheckedDatabaseCleanupSQLiteContainment(t *testing.T) {
	for _, kind := range []string{"parent", "bare parent", "absolute", "symlink parent", "symlink file", "companion directory", "valid nested"} {
		t.Run(kind, func(t *testing.T) {
			root, revision, snapshot := checkedCleanupFixture(t, "sqlite")
			target := "app.db"
			switch kind {
			case "parent":
				target = "../outside.db"
			case "bare parent":
				target = ".."
			case "absolute":
				target = filepath.Join(t.TempDir(), "outside.db")
			case "symlink parent":
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
				target = "link/app.db"
			case "symlink file":
				if err := os.Symlink(filepath.Join(t.TempDir(), "outside.db"), filepath.Join(root, target)); err != nil {
					t.Fatal(err)
				}
			case "companion directory":
				if err := os.Mkdir(filepath.Join(root, target+"-wal"), 0700); err != nil {
					t.Fatal(err)
				}
			case "valid nested":
				if err := os.Mkdir(filepath.Join(root, "data"), 0700); err != nil {
					t.Fatal(err)
				}
				target = "data/app.db"
			}
			snapshot.Databases = []string{target}
			drop := &checkedDropFake{}
			attempted, err := DropDatabasesChecked(context.Background(), root, snapshot, nil, checkedOptions(revision, drop))
			if kind == "valid nested" {
				if err != nil || !attempted || !reflect.DeepEqual(drop.drops, []string{filepath.Join(root, target)}) {
					t.Fatalf("valid contained target: %v %v %v", attempted, err, drop.drops)
				}
			} else if err == nil || attempted || len(drop.drops) != 0 {
				t.Fatalf("unsafe sqlite target: %v %v %v", attempted, err, drop.drops)
			}
		})
	}
}

func TestCheckedDatabaseCleanupProtectedOnlyHasNoEffect(t *testing.T) {
	root, revision, snapshot := checkedCleanupFixture(t, "sqlite")
	snapshot.MainWorktree = true
	snapshot.Databases = []string{"template.db", "shared.db"}
	drop := &checkedDropFake{}
	attempted, err := DropDatabasesChecked(context.Background(), root, snapshot, map[string]bool{"shared.db": true}, checkedOptions(revision, drop))
	if err != nil || attempted || len(drop.drops) != 0 {
		t.Fatalf("protected databases: %v %v %v", attempted, err, drop.drops)
	}
}
