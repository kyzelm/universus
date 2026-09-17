package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User is a row of `users` as anything outside this file is allowed to see it.
// **The password hash is deliberately not in it**: a struct that carries one is
// a struct that eventually gets marshalled to JSON by somebody in a hurry.
type User struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// errNoUser is "there is no such account", and it is returned for a missing
// email and never for a wrong password — telling those two apart is the caller's
// job, and the login handler deliberately does not.
var errNoUser = errors.New("no such user")

// errTaken is a unique index saying no: the email or the display name is
// already somebody's.
var errTaken = errors.New("already taken")

// store is what the handlers need from the database and nothing else.
//
// An interface with one production implementation is usually a smell, and this
// one earns its place: the second implementation is the fake the handler tests
// run against. Auth is the code most worth testing and the code least worth
// needing a live Postgres to test, and the alternative is either no tests or a
// container in CI for four queries.
type store interface {
	// createUser returns errTaken if the email or display name is spoken for.
	createUser(ctx context.Context, email, hash, name string) (User, error)
	// credentials returns the user and their password hash, or errNoUser.
	credentials(ctx context.Context, email string) (User, string, error)
	userByID(ctx context.Context, id int64) (User, error)
	// ratingOf is the account's ladder points, and 0 for an account with no
	// rating row — a new account's rating is zero either way, so there is
	// nothing for the caller to tell apart.
	ratingOf(ctx context.Context, id int64) int
	// rating is the profile view of it: points, tier, and the record.
	rating(ctx context.Context, id int64) Rating
}

// Rating is the ladder as a player sees it (03 Game Design/Game Modes.md).
type Rating struct {
	LP       int    `json:"lp"`
	Tier     int    `json:"tier"`
	TierName string `json:"tierName"`
	Matches  int    `json:"matches"`
	Wins     int    `json:"wins"`
}

type pgStore struct{ pool *pgxpool.Pool }

// createUser writes the account and its starting rating together. A user with
// no rating row is a profile that reads as an error the first time they open
// it, and the two only exist because of each other.
func (s pgStore) createUser(ctx context.Context, email, hash, name string) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var u User
	err = tx.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, display_name)
		 VALUES ($1, $2, $3) RETURNING id, email, display_name`,
		email, hash, name).Scan(&u.ID, &u.Email, &u.DisplayName)
	if err != nil {
		if uniqueViolation(err) {
			return User{}, errTaken
		}
		return User{}, err
	}

	if _, err := tx.Exec(ctx, `INSERT INTO ratings (user_id) VALUES ($1)`, u.ID); err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}

func (s pgStore) credentials(ctx context.Context, email string) (User, string, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, display_name, password_hash FROM users WHERE email = $1`,
		email).Scan(&u.ID, &u.Email, &u.DisplayName, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", errNoUser
	}
	return u, hash, err
}

func (s pgStore) ratingOf(ctx context.Context, id int64) int {
	var lp int
	// A rating that cannot be read is a match nobody gets rather than a player
	// who queues at zero, unless it is read as zero — which is what a brand new
	// account's is. Logged by the caller if it ever matters; the queue's job is
	// to keep working.
	if err := s.pool.QueryRow(ctx, `SELECT lp FROM ratings WHERE user_id = $1`, id).Scan(&lp); err != nil {
		return 0
	}
	return lp
}

func (s pgStore) rating(ctx context.Context, id int64) Rating {
	var r Rating
	// A missing rating row reads as an unplayed account, which is what it is.
	_ = s.pool.QueryRow(ctx,
		`SELECT lp, tier, matches, wins FROM ratings WHERE user_id = $1`, id).
		Scan(&r.LP, &r.Tier, &r.Matches, &r.Wins)
	r.TierName = tierNames[min(max(r.Tier, 0), len(tierNames)-1)]
	return r
}

func (s pgStore) userByID(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, display_name FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.Email, &u.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, errNoUser
	}
	return u, err
}
