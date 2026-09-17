package main

import (
	"strings"
	"testing"
	"testing/fstest"
)

// The shipped migrations have to load, because the alternative is a server that
// starts, connects, and fails on the one step it exists to do at startup.
func TestTheEmbeddedMigrationsLoad(t *testing.T) {
	ms, err := loadMigrations(migrationFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations are embedded")
	}
	if ms[0].version != 1 || ms[0].name != "init" {
		t.Errorf("the first migration is %03d_%s, want 001_init", ms[0].version, ms[0].name)
	}
	for i, m := range ms {
		if m.version != i+1 {
			t.Errorf("migration %d is version %d: the numbering has a hole in it", i, m.version)
		}
	}

	// Every table the schema note specifies, because a migration that parses
	// and creates nothing is the failure with no symptom.
	sql := ms[0].sql
	for _, table := range []string{
		"users", "ratings", "matches", "match_submissions", "verification_jobs",
	} {
		if !strings.Contains(sql, "CREATE TABLE "+table) {
			t.Errorf("001_init does not create %q", table)
		}
	}
}

func TestMigrationsAreOrderedByNumberNotByName(t *testing.T) {
	ms, err := loadMigrations(fstest.MapFS{
		"migrations/010_later.sql":  {Data: []byte("SELECT 10")},
		"migrations/002_middle.sql": {Data: []byte("SELECT 2")},
		"migrations/001_first.sql":  {Data: []byte("SELECT 1")},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The case the filename numbering exists for: "010" sorts before "002" in
	// no ordering anybody wants, and lexical order would apply them backwards.
	var got []int
	for _, m := range ms {
		got = append(got, m.version)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 10 {
		t.Errorf("applied in order %v, want [1 2 10]", got)
	}
}

// **Two files claiming the same version is the ordinary way this goes wrong**:
// two branches each adding 002. Applying one of them and recording the version
// leaves the other silently skipped forever, on one database and not another.
func TestDuplicateVersionsAreRefused(t *testing.T) {
	_, err := loadMigrations(fstest.MapFS{
		"migrations/002_one.sql": {Data: []byte("SELECT 1")},
		"migrations/002_two.sql": {Data: []byte("SELECT 2")},
	})
	if err == nil || !strings.Contains(err.Error(), "both version 2") {
		t.Errorf("got %v, want a complaint about two version 2s", err)
	}
}

func TestAnUnnumberedFileIsRefused(t *testing.T) {
	for _, name := range []string{"migrations/fix.sql", "migrations/2_init.sql", "migrations/002-init.sql"} {
		if _, err := loadMigrations(fstest.MapFS{name: {Data: []byte("SELECT 1")}}); err == nil {
			t.Errorf("%q was accepted; it has no place in an ordering", name)
		}
	}
}
