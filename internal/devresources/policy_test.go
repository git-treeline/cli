package devresources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

type fixture struct {
	home, private, normal                 string
	normalConfig, privateConfig, registry map[string]any
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{home: home, private: filepath.Join(home, ".treeline-dev", "gtl"), normal: filepath.Join(home, "Library", "Application Support", "git-treeline")}
	f.normalConfig = map[string]any{"port": map[string]any{"base": 3002, "increment": 10, "reservations": map[string]any{"@treeline-dev-qa/slot-01": 42000, "@treeline-dev-qa/slot-02": 42010, "@treeline-dev-qa/slot-03": 42020, "@treeline-dev-qa/slot-04": 42030}}}
	f.privateConfig = map[string]any{"port": map[string]any{"base": 42000, "increment": 10, "reservations": map[string]any{"fixture": 42000, "fixture/qa-one": 42010, "fixture/qa/two": 42020, "other/main": 42030}}, "router": map[string]any{"mode": "disabled"}}
	entries := []any{}
	for i := range 53 {
		entries = append(entries, map[string]any{"worktree": "/normal/retained", "project": "normal", "port": 3002 + i*10, "ports": []any{3002 + i*10}, "port_1": 3002 + i*10})
	}
	f.registry = map[string]any{"version": 2, "allocations": entries}
	for _, dir := range []string{f.normal, f.private} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("GTL_HOME", f.private)
	t.Setenv("TREELINE_MANAGED", "app")
	t.Setenv("TREELINE_CHANNEL", "dev")
	f.write(t)
	return f
}
func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func (f fixture) write(t *testing.T) {
	t.Helper()
	writeJSON(t, filepath.Join(f.normal, "config.json"), f.normalConfig)
	writeJSON(t, filepath.Join(f.private, "config.json"), f.privateConfig)
	writeJSON(t, filepath.Join(f.normal, "registry.json"), f.registry)
}
func portMap(config map[string]any) map[string]any { return config["port"].(map[string]any) }
func reservations(config map[string]any) map[string]any {
	return portMap(config)["reservations"].(map[string]any)
}
func fixtureAllocation() map[string]any {
	return map[string]any{"project": "fixture", "branch": "qa-one", "port": 42010, "ports": []any{42010, 42011}, "port_1": 42010, "port_2": 42011, "database": "", "databases": []any{}, "redis_db": nil, "redis_prefix": ""}
}

func TestCheckAndExplicitPortsAreReadOnly(t *testing.T) {
	f := newFixture(t)
	before := map[string][]byte{}
	for _, path := range []string{filepath.Join(f.normal, "config.json"), filepath.Join(f.normal, "registry.json"), filepath.Join(f.private, "config.json")} {
		before[path], _ = os.ReadFile(path)
	}
	p, err := Check()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		project, branch string
		main            bool
		count           int
		want            []int
	}{{"fixture", "main", true, 1, []int{42000}}, {"fixture", "qa-one", false, 2, []int{42010, 42011}}, {"fixture", "qa/two", false, 1, []int{42020}}, {"other", "main", true, 10, []int{42030, 42031, 42032, 42033, 42034, 42035, 42036, 42037, 42038, 42039}}} {
		ports, err := p.Ports(tc.project, tc.branch, tc.main, tc.count)
		if err != nil || !reflect.DeepEqual(ports, tc.want) {
			t.Fatalf("ports: %v %v", ports, err)
		}
	}
	for _, tc := range []struct {
		project, branch string
		main            bool
		count           int
	}{{"fixture", "missing", false, 1}, {"unknown", "main", true, 1}, {"fixture", "", false, 1}, {"fixture", "qa-one", false, 0}, {"fixture", "qa-one", false, 11}, {"@treeline-dev-qa", "slot-01", true, 1}} {
		if _, err := p.Ports(tc.project, tc.branch, tc.main, tc.count); err == nil {
			t.Fatal("unlisted allocation admitted")
		}
	}
	if err := p.ValidateAllocation(fixtureAllocation()); err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		got, _ := os.ReadFile(path)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("policy changed state")
		}
	}
	if _, err := os.Stat(filepath.Join(f.private, "registry.json")); !os.IsNotExist(err) {
		t.Fatal("private registry created")
	}
}
func TestCheckRejectsConfigurationDrift(t *testing.T) {
	cases := map[string]func(*fixture){
		"private user editor":   func(f *fixture) { f.privateConfig["editor"] = map[string]any{"command": "code"} },
		"private user redis":    func(f *fixture) { f.privateConfig["redis"] = map[string]any{"strategy": "database"} },
		"normal router overlap": func(f *fixture) { f.normalConfig["router"] = map[string]any{"port": 42000} },
		"normal increment":      func(f *fixture) { portMap(f.normalConfig)["increment"] = 9 },
		"missing sentinel":      func(f *fixture) { delete(reservations(f.normalConfig), "@treeline-dev-qa/slot-01") },
		"changed sentinel":      func(f *fixture) { reservations(f.normalConfig)["@treeline-dev-qa/slot-01"] = 42001 },
		"ordinary overlap":      func(f *fixture) { reservations(f.normalConfig)["normal/branch"] = 41999 },
		"fractional":            func(f *fixture) { reservations(f.normalConfig)["@treeline-dev-qa/slot-01"] = 42000.5 },
		"nonnumeric":            func(f *fixture) { reservations(f.normalConfig)["other"] = "42000" },
		"private duplicate":     func(f *fixture) { reservations(f.privateConfig)["other/main"] = 42000 },
		"private sentinel": func(f *fixture) {
			delete(reservations(f.privateConfig), "fixture")
			reservations(f.privateConfig)["@treeline-dev-qa/slot-01"] = 42000
		},
		"private outside":       func(f *fixture) { reservations(f.privateConfig)["fixture"] = 42040 },
		"private interior":      func(f *fixture) { reservations(f.privateConfig)["fixture"] = 42001 },
		"private increment":     func(f *fixture) { portMap(f.privateConfig)["increment"] = 1 },
		"router enabled":        func(f *fixture) { f.privateConfig["router"] = map[string]any{"mode": "enabled"} },
		"empty mappings":        func(f *fixture) { portMap(f.privateConfig)["reservations"] = map[string]any{} },
		"bad registry version":  func(f *fixture) { f.registry["version"] = 5 },
		"bad registry envelope": func(f *fixture) { f.registry["allocations"] = nil },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			change(&f)
			f.write(t)
			if _, err := Check(); err == nil {
				t.Fatal("drift admitted")
			}
		})
	}
}
func TestCheckRejectsNormalPortClaimsAndMalformedFields(t *testing.T) {
	cases := map[string]map[string]any{"scalar": {"port": 42000}, "secondary": {"ports": []any{3100, 42039}}, "contradictory scalar": {"port": 42000, "ports": []any{3100}}, "contradictory numbered": {"ports": []any{3100}, "port_1": 42000}, "fractional": {"ports": []any{3100.5}}, "string": {"ports": []any{"3100"}}, "empty": {"ports": []any{}}, "nil": {"ports": nil}, "duplicate": {"ports": []any{3100, 3100}}, "extra numbered": {"port": 3100, "port_2": 42000}, "malformed numbered": {"port": 3100, "port_bad": 42000}, "no ports": {"project": "normal"}}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.registry["allocations"] = []any{entry}
			f.write(t)
			if _, err := Check(); err == nil {
				t.Fatal("normal allocation admitted")
			}
		})
	}
}
func TestCheckRejectsUnsafeFilesAndMalformedJSON(t *testing.T) {
	for _, kind := range []string{"missing", "symlink-file", "hardlink-file", "symlink-dir", "writable-file", "writable-parent", "readable-private", "duplicate-json", "trailing-json", "corrupt", "wrong-home"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			path := filepath.Join(f.normal, "config.json")
			switch kind {
			case "missing":
				os.Remove(path)
			case "hardlink-file":
				if err := os.Link(path, path+".linked"); err != nil {
					t.Fatal(err)
				}
			case "symlink-file":
				os.Rename(path, path+".real")
				os.Symlink(path+".real", path)
			case "symlink-dir":
				os.Rename(f.normal, f.normal+".real")
				os.Symlink(f.normal+".real", f.normal)
			case "writable-file":
				os.Chmod(path, 0660)
			case "writable-parent":
				os.Chmod(filepath.Dir(f.normal), 0770)
			case "readable-private":
				os.Chmod(filepath.Join(f.private, "config.json"), 0644)
			case "duplicate-json":
				os.WriteFile(path, []byte(`{"port":{},"port":{}}`), 0600)
			case "trailing-json":
				data, _ := os.ReadFile(path)
				os.WriteFile(path, append(data, []byte(` {}`)...), 0600)
			case "corrupt":
				os.WriteFile(path, []byte(`{`), 0600)
			case "wrong-home":
				t.Setenv("GTL_HOME", f.normal)
			}
			if _, err := Check(); err == nil {
				t.Fatal("unsafe state admitted")
			}
		})
	}
}
func TestPolicyRejectsRetainedResourceAndPortDrift(t *testing.T) {
	newFixture(t)
	p, err := Check()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(map[string]any){"database": func(a map[string]any) { a["database"] = "db" }, "databases": func(a map[string]any) { a["databases"] = []any{"db"} }, "extra": func(a map[string]any) { a["database_extra"] = map[string]any{"audit": "db"} }, "redis zero": func(a map[string]any) { a["redis_db"] = 0 }, "redis prefix": func(a map[string]any) { a["redis_prefix"] = "fixture:branch" }, "branch": func(a map[string]any) { a["branch"] = "other" }, "outside": func(a map[string]any) { a["ports"] = []any{3002, 3003} }, "noncontiguous": func(a map[string]any) { a["ports"] = []any{42010, 42012}; a["port_2"] = 42012 }, "scalar mismatch": func(a map[string]any) { a["port"] = 42020 }, "wrong main": func(a map[string]any) { a["main_worktree"] = "true" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			a := fixtureAllocation()
			change(a)
			if p.ValidateAllocation(a) == nil {
				t.Fatal("retained drift admitted")
			}
		})
	}
}
func TestInactiveCheckDoesNotReadState(t *testing.T) {
	for _, mode := range [][2]string{{"", ""}, {"app", "stable"}, {"", "dev"}, {"other", "dev"}} {
		t.Run(mode[0]+mode[1], func(t *testing.T) {
			t.Setenv("TREELINE_MANAGED", mode[0])
			t.Setenv("TREELINE_CHANNEL", mode[1])
			t.Setenv("HOME", "/absent")
			t.Setenv("GTL_HOME", "/absent")
			p, err := Check()
			if Active() || p != nil || err != nil {
				t.Fatal("other channel affected")
			}
		})
	}
}

func TestValidateProject(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, config.ProjectConfigFile)
	for name, body := range map[string]string{"http": "project: fixture\nport_count: 1\nenv: {PORT: '{port}'}\ncommands: {start: 'node server.js'}\n", "database": "project: fixture\ndatabase: {template: shared}\n", "redis": "project: fixture\nredis: {url: 'redis://localhost'}\n", "hook": "project: fixture\nhooks: {post_setup: [touch shared]}\n", "setup": "project: fixture\ncommands: {setup: [touch shared]}\n", "stop": "project: fixture\ncommands: {stop: 'killall node'}\n", "provision": "project: fixture\nprovision: {}\n", "env escape": "project: fixture\nenv_file: ../outside\n", "copy": "project: fixture\ncopy_files: [.env]\n", "related": "project: fixture\nrelated: [normal]\n", "editor": "project: fixture\neditor: {command: code}\n"} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			err := ValidateProject(config.LoadProjectConfigReadOnlyQuiet(root))
			if (name == "http") != (err == nil) {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}
