package main

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The migration runner and the store against a real Postgres.
//
// **Skipped unless TEST_DATABASE_URL is set**, because the unit tests beside it
// must run on any machine with a Go toolchain and nothing else. What this adds
// is the half a fake cannot check: that the SQL is valid, that the constraints
// the schema promises are really enforced, and that running the migrations
// twice is not running them twice.
//
//	docker run --rm -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=universus -p 55432:5432 postgres:16-alpine
//	TEST_DATABASE_URL=postgres://postgres:dev@localhost:55432/universus go test ./server/
func testPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the database tests")
	}

	ctx := context.Background()
	pool, err := openDB(ctx, url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)

	// Every run starts from nothing, so a test can assert what migrating does
	// rather than what migrating did the first time somebody ran it.
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatalf("resetting the schema: %v", err)
	}
	return ctx, pool
}

func TestMigrationsApplyAndAreNotAppliedTwice(t *testing.T) {
	ctx, p := testPool(t)

	n, err := migrate(ctx, p, migrationFiles)
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if n == 0 {
		t.Fatal("a fresh database applied no migrations")
	}

	// The property the whole bookkeeping table exists for: a second startup is
	// not a second schema.
	again, err := migrate(ctx, p, migrationFiles)
	if err != nil {
		t.Fatalf("migrating again: %v", err)
	}
	if again != 0 {
		t.Errorf("a second run applied %d migration(s)", again)
	}

	var tables int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	// Five from the schema note, plus schema_migrations.
	if tables != 6 {
		t.Errorf("%d tables after migrating, want 6", tables)
	}
}

func TestTheStoreEnforcesWhatTheSchemaPromises(t *testing.T) {
	ctx, p := testPool(t)
	if _, err := migrate(ctx, p, migrationFiles); err != nil {
		t.Fatal(err)
	}
	s := pgStore{pool: p}

	u, err := s.createUser(ctx, "player@example.com", "$2a$12$hash", "player-one")
	if err != nil {
		t.Fatalf("createUser: %v", err)
	}

	// A rating row is written with the account, or the first profile the user
	// opens reads as an error.
	var lp int
	if err := p.QueryRow(ctx, `SELECT lp FROM ratings WHERE user_id = $1`, u.ID).Scan(&lp); err != nil {
		t.Fatalf("no rating row: %v", err)
	}

	// The unique indexes, told from a database that is on fire.
	if _, err := s.createUser(ctx, "player@example.com", "x", "somebody-else"); err != errTaken {
		t.Errorf("a duplicate email gave %v, want errTaken", err)
	}
	if _, err := s.createUser(ctx, "other@example.com", "x", "player-one"); err != errTaken {
		t.Errorf("a duplicate display name gave %v, want errTaken", err)
	}

	got, hash, err := s.credentials(ctx, "player@example.com")
	if err != nil || got.ID != u.ID || hash != "$2a$12$hash" {
		t.Errorf("credentials: %+v %q %v", got, hash, err)
	}
	if _, _, err := s.credentials(ctx, "nobody@example.com"); err != errNoUser {
		t.Errorf("an unknown email gave %v, want errNoUser", err)
	}
	if _, err := s.userByID(ctx, u.ID+999); err != errNoUser {
		t.Errorf("an unknown id gave %v, want errNoUser", err)
	}
}
