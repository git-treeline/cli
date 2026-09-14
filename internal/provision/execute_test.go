package provision

import (
	"errors"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

// fakeSys records effect calls and answers probes from configured state.
type fakeSys struct {
	installed map[string]bool // apt packages present
	dbs       map[string]bool // existing databases
	misePath  bool

	calls []string
}

func newFakeSys() *fakeSys {
	return &fakeSys{installed: map[string]bool{}, dbs: map[string]bool{}}
}

func (f *fakeSys) deps(goos string) Deps {
	return Deps{
		GOOS:       goos,
		FileExists: func(string) bool { return false },
		LookPath: func(bin string) (string, error) {
			if bin == "mise" && f.misePath {
				return "/usr/bin/mise", nil
			}
			return "", errors.New("not found")
		},
		PackageInstalled: func(pkg string) (bool, error) { return f.installed[pkg], nil },
		AptUpdate: func() error {
			f.calls = append(f.calls, "apt-update")
			return nil
		},
		AptInstall: func(pkgs []string) error {
			f.calls = append(f.calls, "apt-install:"+strings.Join(pkgs, ","))
			for _, p := range pkgs {
				f.installed[p] = true
			}
			return nil
		},
		ServiceEnable: func(name string) error {
			f.calls = append(f.calls, "enable:"+name)
			return nil
		},
		RunInDir: func(dir, command string) error {
			f.calls = append(f.calls, "run:"+command)
			return nil
		},
		DBExists: func(name string) (bool, error) { return f.dbs[name], nil },
		CreateDB: func(name string) error {
			f.calls = append(f.calls, "createdb:"+name)
			f.dbs[name] = true
			return nil
		},
		DropDB: func(name string) error {
			f.calls = append(f.calls, "dropdb:"+name)
			delete(f.dbs, name)
			return nil
		},
		HydrateFromSource: func(template, env string) error {
			f.calls = append(f.calls, "hydrate-source:"+template+"<-"+env)
			f.dbs[template] = true
			return nil
		},
		Log:  func(string, ...any) {},
		Warn: func(string, ...any) {},
	}
}

func (f *fakeSys) called(sub string) bool {
	for _, c := range f.calls {
		if c == sub {
			return true
		}
	}
	return false
}

func TestRun_Apt_InstallsOnlyMissing(t *testing.T) {
	f := newFakeSys()
	f.installed["libvips"] = true // already installed
	actions := []Action{{Kind: ActionApt, Packages: []string{"libvips", "imagemagick"}}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if !f.called("apt-install:imagemagick") {
		t.Errorf("expected only imagemagick installed, calls=%v", f.calls)
	}
	if f.called("apt-install:libvips,imagemagick") {
		t.Errorf("should not reinstall libvips, calls=%v", f.calls)
	}
}

func TestRun_Apt_AllInstalled_NoOp(t *testing.T) {
	f := newFakeSys()
	f.installed["libvips"] = true
	actions := []Action{{Kind: ActionApt, Packages: []string{"libvips"}}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Errorf("expected no effects, got %v", f.calls)
	}
}

func TestRun_Apt_DarwinSkip_NoEffects(t *testing.T) {
	f := newFakeSys()
	actions := PlanConfig(config.ProvisionConfig{Present: true, Apt: []string{"libvips"}}, "darwin")
	if err := Run(actions, "/repo", f.deps("darwin")); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Errorf("darwin apt should make no effects, got %v", f.calls)
	}
}

func TestRun_AptUpdate_RunsOnceBeforeFirstInstall(t *testing.T) {
	f := newFakeSys()
	// Two install-triggering steps (apt packages + a service package) on a fresh
	// host: update must run exactly once, before the first install.
	actions := []Action{
		{Kind: ActionApt, Packages: []string{"libvips"}},
		{Kind: ActionService, Packages: []string{"redis-server"}},
	}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if got := count(f.calls, "apt-update"); got != 1 {
		t.Fatalf("expected apt-update exactly once, got %d: %v", got, f.calls)
	}
	first := idxOf(f.calls, "apt-install:libvips")
	if first < 0 || idxOf(f.calls, "apt-update") > first {
		t.Errorf("apt-update must precede the first install: %v", f.calls)
	}
}

func TestRun_AptUpdate_SkippedWhenNothingToInstall(t *testing.T) {
	f := newFakeSys()
	f.installed["libvips"] = true
	f.installed["redis-server"] = true
	// Everything already present: an idempotent re-run installs nothing, so the
	// lazy update must never fire — no-op runs stay fast and sudo-free.
	actions := []Action{
		{Kind: ActionApt, Packages: []string{"libvips"}},
		{Kind: ActionService, Packages: []string{"redis-server"}},
	}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if f.called("apt-update") {
		t.Errorf("apt-update should not run when nothing is installed: %v", f.calls)
	}
}

func TestRun_Services_InstallThenEnable(t *testing.T) {
	f := newFakeSys()
	actions := []Action{{Kind: ActionService, Packages: []string{"redis-server"}}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if !f.called("apt-install:redis-server") || !f.called("enable:redis-server") {
		t.Errorf("expected install+enable, calls=%v", f.calls)
	}
	// enable must come after install.
	if idxOf(f.calls, "enable:redis-server") < idxOf(f.calls, "apt-install:redis-server") {
		t.Errorf("enable before install: %v", f.calls)
	}
}

func TestRun_Services_AlreadyInstalled_EnableOnly(t *testing.T) {
	f := newFakeSys()
	f.installed["redis-server"] = true
	actions := []Action{{Kind: ActionService, Packages: []string{"redis-server"}}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if f.called("apt-install:redis-server") {
		t.Errorf("should not reinstall, calls=%v", f.calls)
	}
	if !f.called("enable:redis-server") {
		t.Errorf("expected enable, calls=%v", f.calls)
	}
}

func TestRun_Runtime_MisePresent_RunsInstall(t *testing.T) {
	f := newFakeSys()
	f.misePath = true
	actions := []Action{{Kind: ActionRuntime, VersionFiles: []string{".ruby-version"}}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if !f.called("run:mise install") {
		t.Errorf("expected mise install, calls=%v", f.calls)
	}
}

func TestRun_Runtime_NoMise_Warns_NoError(t *testing.T) {
	f := newFakeSys()
	f.misePath = false
	actions := []Action{{Kind: ActionRuntime, VersionFiles: []string{".ruby-version"}}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if f.called("run:mise install") {
		t.Errorf("should not run mise when absent, calls=%v", f.calls)
	}
}

func TestRun_Database_Exists_Skips(t *testing.T) {
	f := newFakeSys()
	f.dbs["app_dev"] = true
	actions := []Action{{Kind: ActionDatabase, DBTemplate: "app_dev", DBMode: DBModeEmpty}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if f.called("createdb:app_dev") {
		t.Errorf("should not create existing db, calls=%v", f.calls)
	}
}

func TestRun_Database_SourceMode(t *testing.T) {
	f := newFakeSys()
	actions := []Action{{Kind: ActionDatabase, DBTemplate: "app_dev", DBMode: DBModeSource, DBSource: "production"}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if !f.called("hydrate-source:app_dev<-production") {
		t.Errorf("expected source hydration, calls=%v", f.calls)
	}
}

func TestRun_Database_HydrateMode_CreateThenCommand(t *testing.T) {
	f := newFakeSys()
	actions := []Action{{Kind: ActionDatabase, DBTemplate: "app_dev", DBMode: DBModeHydrate, DBHydrate: "bin/rails db:schema:load"}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if !f.called("createdb:app_dev") || !f.called("run:bin/rails db:schema:load") {
		t.Errorf("expected create+hydrate, calls=%v", f.calls)
	}
	if idxOf(f.calls, "run:bin/rails db:schema:load") < idxOf(f.calls, "createdb:app_dev") {
		t.Errorf("hydrate before create: %v", f.calls)
	}
}

func TestRun_Database_EmptyMode_CreatesEmpty(t *testing.T) {
	f := newFakeSys()
	actions := []Action{{Kind: ActionDatabase, DBTemplate: "app_dev", DBMode: DBModeEmpty}}
	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatal(err)
	}
	if !f.called("createdb:app_dev") {
		t.Errorf("expected createdb, calls=%v", f.calls)
	}
}

func TestRun_Database_HydrateFailureRemovesCreatedTemplateAndCanRetry(t *testing.T) {
	f := newFakeSys()
	actions := []Action{{Kind: ActionDatabase, DBTemplate: "app_dev", DBMode: DBModeHydrate, DBHydrate: "bin/hydrate"}}
	d := f.deps("linux")
	d.RunInDir = func(string, string) error {
		f.calls = append(f.calls, "run:bin/hydrate")
		return errors.New("hydrate failed")
	}
	if err := Run(actions, "/repo", d); err == nil {
		t.Fatal("expected hydrate failure")
	}
	if f.dbs["app_dev"] || !f.called("dropdb:app_dev") {
		t.Errorf("failed hydration left template behind: dbs=%v calls=%v", f.dbs, f.calls)
	}

	if err := Run(actions, "/repo", f.deps("linux")); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if !f.dbs["app_dev"] || count(f.calls, "run:bin/hydrate") != 2 {
		t.Errorf("retry did not recreate and hydrate template: dbs=%v calls=%v", f.dbs, f.calls)
	}
}

func TestRun_Database_SourceFailureRemovesOnlyNewTemplateAndReportsCleanupError(t *testing.T) {
	f := newFakeSys()
	actions := []Action{{Kind: ActionDatabase, DBTemplate: "app_dev", DBMode: DBModeSource, DBSource: "production"}}
	d := f.deps("linux")
	d.HydrateFromSource = func(template, env string) error {
		f.dbs[template] = true
		return errors.New("restore failed")
	}
	if err := Run(actions, "/repo", d); err == nil {
		t.Fatal("expected source hydration failure")
	}
	if f.dbs["app_dev"] || !f.called("dropdb:app_dev") {
		t.Errorf("failed source hydration left template behind: dbs=%v calls=%v", f.dbs, f.calls)
	}

	f.dbs["app_dev"] = true
	d = f.deps("linux")
	d.HydrateFromSource = func(string, string) error { return errors.New("should not run") }
	if err := Run(actions, "/repo", d); err != nil {
		t.Fatal(err)
	}
	if count(f.calls, "dropdb:app_dev") != 1 {
		t.Errorf("preexisting template was cleaned up: calls=%v", f.calls)
	}

	f = newFakeSys()
	d = f.deps("linux")
	d.HydrateFromSource = func(template, env string) error {
		f.dbs[template] = true
		return errors.New("restore failed")
	}
	d.DropDB = func(string) error { return errors.New("drop failed") }
	err := Run(actions, "/repo", d)
	if err == nil || !strings.Contains(err.Error(), "removing incomplete template") || !strings.Contains(err.Error(), "drop failed") {
		t.Errorf("cleanup error was not visible: %v", err)
	}
}

func idxOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func count(s []string, v string) int {
	n := 0
	for _, x := range s {
		if x == v {
			n++
		}
	}
	return n
}
