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
