package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrations are numbered .sql files applied by this binary at startup. No
// framework (CLAUDE.md, 02 Architecture/Database Schema.md): the whole of what
// a framework would give us is the applied-set table below and a sort, and the
// whole of what it would cost is a second tool to install on the deploy host.
//
// Embedded rather than read from disk, for the same reason the character data
// is: the binary carries its own schema, so a container that starts is a
// container that can migrate, and there is no directory to forget to copy.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// The filename is the version and the description, in that order, and the
// loader refuses anything else — a file named `fix.sql` has no place in an
// ordering, and a directory where some files are numbered and some are not is
// one where the order depends on who looks.
var migrationName = regexp.MustCompile(`^(\d{3})_([a-z0-9_]+)\.sql$`)

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded directory in version order.
//
// **Duplicate versions are refused rather than ordered.** Two people adding
// `002_` on separate branches is the ordinary way this goes wrong, and the
// symptom without this check is a database that applied one of them and a
// developer who cannot tell which.
func loadMigrations(dir fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(dir, "migrations")
	if err != nil {
		return nil, err
	}

	out := make([]migration, 0, len(entries))
	seen := map[int]string{}
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %q is not <nnn>_<name>.sql", e.Name())
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("migration %q: %w", e.Name(), err)
		}
		if first, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %q and %q are both version %d", first, e.Name(), version)
		}
		seen[version] = e.Name()

		body, err := fs.ReadFile(dir, "migrations/"+e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: m[2], sql: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// migrate applies every migration the database has not seen, in order.
//
// **One transaction per migration, and the record of it is written inside that
// same transaction.** A migration that fails halfway leaves the database as it
// was and the run reports which file broke; a migration that succeeds cannot be
// applied twice even if the process dies between the DDL and the bookkeeping,
// because there is no between.
func migrate(ctx context.Context, pool *pgxpool.Pool, dir fs.FS) (int, error) {
	ms, err := loadMigrations(dir)
	if err != nil {
		return 0, err
	}

	// The bookkeeping table is created outside the loop and not by a migration
	// of its own: a migration runner that needs a migration to start is a
	// chicken-and-egg problem with a manual step in it.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return 0, fmt.Errorf("creating schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return 0, fmt.Errorf("reading schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return 0, err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	run := 0
	for _, m := range ms {
		if applied[m.version] {
			continue
		}
		if err := applyOne(ctx, pool, m); err != nil {
			return run, fmt.Errorf("migration %03d_%s: %w", m.version, m.name, err)
		}
		run++
	}
	return run, nil
}

func applyOne(ctx context.Context, pool *pgxpool.Pool, m migration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	// Rollback after a commit is a no-op, so this is the only unwind path and
	// there is no branch that can forget it.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.version, m.name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// openDB connects and migrates. A nil pool is not an error: the room, the
// relay and every offline mode work without a database, and three of the five
// game modes require no account at all (03 Game Design/Game Modes.md). Losing
// the ranked ladder because Postgres is down should not lose the demo.
func openDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, nil
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: %w", err)
	}
	// Single digits of concurrent users, and a free-tier Postgres with a
	// connection cap measured in tens. A default-sized pool per instance is
	// how a small deployment runs out of connections doing nothing.
	cfg.MaxConns = 8

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connecting: %w", err)
	}
	return pool, nil
}

// uniqueViolation reports Postgres's "a unique index said no" (SQLSTATE 23505).
// Told apart from a database that is on fire because they are the same error to
// anything that only checks for non-nil, and one of them is a 409 while the
// other is a 500.
func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
