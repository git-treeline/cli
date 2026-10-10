package database

import (
	"reflect"
	"strings"
	"testing"
)

func TestCheckedCleanupRejectsImplicitAndAmbientConnections(t *testing.T) {
	explicit := []string{"-h", "localhost", "-p", "5432", "-U", "treeline"}
	for _, args := range [][]string{nil, {"-h", "localhost"}, {"-h", "localhost", "-p", "5432", "-p", "5432"}, {"-h", "a,b", "-p", "5432", "-U", "treeline"}, {"-h", "localhost", "-p", "05432", "-U", "treeline"}, {"-h", "localhost", "-p", "65536", "-U", "treeline"}} {
		if _, err := checkedCleanupAdapter("postgresql", args, nil); err == nil {
			t.Fatalf("accepted implicit or ambiguous connection: %q", args)
		}
	}
	for _, variable := range []string{"PGHOST", "PGHOSTADDR", "PGPORT", "PGUSER", "PGDATABASE", "PGSERVICE", "PGSERVICEFILE", "PGOPTIONS", "PGLOADBALANCEHOSTS", "PGUNKNOWNFUTURESETTING"} {
		_, err := checkedCleanupAdapter("postgresql", explicit, []string{variable + "=private"})
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("ambient input accepted or disclosed: %s %v", variable, err)
		}
	}
}

func TestCheckedCleanupCapturesOnlyExplicitCoordinatesAndPrivateEnvironment(t *testing.T) {
	args := []string{"-h", "localhost", "-p", "5432", "-U", "treeline"}
	env := []string{"PATH=/usr/bin", "PGPASSWORD=private", "PGPASSFILE=/private/credentials"}
	adapter, err := checkedCleanupAdapter("postgresql", args, env)
	if err != nil {
		t.Fatal(err)
	}
	pg := adapter.(*PostgreSQL)
	args[1] = "replacement"
	env[1] = "PGHOST=other"
	// Construct only; never start a database process.
	command := pg.command("dropdb", append(pg.ConnArgs, "--if-exists", "fixture")...)
	if !reflect.DeepEqual(command.Args[1:], []string{"-h", "localhost", "-p", "5432", "-U", "treeline", "--if-exists", "fixture"}) {
		t.Fatal(command.Args)
	}
	if !reflect.DeepEqual(command.Env, []string{"PATH=/usr/bin", "PGPASSWORD=private", "PGPASSFILE=/private/credentials"}) {
		t.Fatal("environment changed after preparation")
	}
	empty, err := checkedCleanupAdapter("postgresql", []string{"-h", "localhost", "-p", "5432", "-U", "treeline"}, nil)
	if err != nil || empty.(*PostgreSQL).command("dropdb").Env == nil {
		t.Fatal("empty environment inherited ambient state", err)
	}
	sqlite, err := checkedCleanupAdapter("sqlite", nil, []string{"PGHOST=elsewhere"})
	if _, ok := sqlite.(*SQLite); err != nil || !ok {
		t.Fatal("unrelated sqlite connection rejected", err)
	}
}

func TestCheckedPostgreSQLCloneAndDropStayOnExplicitConnection(t *testing.T) {
	t.Setenv("GTL_HOME", t.TempDir())
	args := []string{"-h", "127.0.0.1", "-p", "5432", "-U", "qa"}
	adapter, err := checkedCleanupAdapter("postgresql", args, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatal(err)
	}
	pg := adapter.(*PostgreSQL)
	var calls []cmdCall
	pg.execRun = func(name string, args ...string) error {
		calls = append(calls, cmdCall{name: name, args: append([]string(nil), args...)})
		return nil
	}
	const template = "treeline_dev_host_migration_qa_template"
	const target = template + "_qa_one"
	if err := pg.Clone(template, target); err != nil {
		t.Fatal(err)
	}
	if err := pg.Drop(target); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatal(calls)
	}
	for _, call := range calls {
		if len(call.args) < 6 || !reflect.DeepEqual(call.args[:6], args) {
			t.Fatal("connection changed", call)
		}
	}
	if calls[0].name != "psql" || !strings.Contains(calls[0].args[len(calls[0].args)-1], "datname = '"+template+"'") {
		t.Fatal("template session scope changed", calls[0])
	}
	if calls[1].name != "createdb" || !reflect.DeepEqual(calls[1].args[6:], []string{target, "--template", template}) {
		t.Fatal("clone changed", calls[1])
	}
	if calls[2].name != "dropdb" || !reflect.DeepEqual(calls[2].args[6:], []string{"--if-exists", target}) {
		t.Fatal("cleanup changed", calls[2])
	}
}
