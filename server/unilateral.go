package main

// A match only one side ever reported on
// (02 Architecture/Disconnect and Match Integrity.md).
//
// **The surviving client's unilateral upload counts** (D52). The alternative is
// that every ranked match can be voided by quitting, which is a worse exploit
// than the one it would prevent.
//
// **And it is exploitable, which the thesis has to say rather than hide.** A
// cheater can force a disconnect and submit a fabricated log. What constrains
// them is that verification still runs: a fabricated log has to be a *valid
// simulation* that actually produces the claimed result when the server replays
// it. That reduces forgery from "claim any outcome" to "claim an outcome I
// could plausibly have achieved" — a real reduction, not a fix. The statistical
// guard beside it is the unilateral-win rate, which is a review signal and
// never an automatic penalty.

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// How long a lone submission waits for its pair before it is accepted on
	// its own. **The wait is the whole mechanism**: without it, whichever
	// client's packet arrived first would decide the match, and a quitter who
	// fabricates a counter-claim would be racing the player they quit on.
	// With it, both claims are in hand before anything is settled, and two
	// disagreeing claims void the match as they would in any other case.
	unilateralGrace = 30 * time.Second

	// How often to look for one that has waited long enough. Nobody is watching
	// the ladder this closely, and the survivor has already seen their win on
	// their own screen.
	unilateralPoll = 10 * time.Second
)

// sweepUnilateral settles every match whose single submission has outlived the
// grace period. Reports whether it did any work, so a caller can drain.
func (v verifier) sweepUnilateral(ctx context.Context) bool {
	tx, err := v.matches.pool.Begin(ctx)
	if err != nil {
		log.Printf("unilateral: %v", err)
		return false
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One row, locked, skipping anything another worker is already holding —
	// the same claim discipline the verification queue uses.
	var id int64
	err = tx.QueryRow(ctx,
		`SELECT m.id FROM matches m
		 JOIN match_submissions s ON s.match_id = m.id
		 WHERE m.winner IS NULL
		 GROUP BY m.id
		 HAVING count(*) = 1 AND min(s.received_at) < now() - $1::interval
		 ORDER BY m.id
		 LIMIT 1`, unilateralGrace.String()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		log.Printf("unilateral: looking for one: %v", err)
		return false
	}

	// Re-read under a row lock before acting: between the aggregate above and
	// here, the other player's upload may have arrived and settled it.
	var row matchRow
	var winner *int
	if err := tx.QueryRow(ctx,
		`SELECT id, mode, p1_id, p2_id, winner FROM matches WHERE id = $1 FOR UPDATE`, id).
		Scan(&row.id, &row.mode, &row.p1, &row.p2, &winner); err != nil {
		log.Printf("unilateral: match %d: %v", id, err)
		return false
	}
	if winner != nil {
		return true // somebody else settled it; there may be more to do
	}

	var s submission
	var stored storedResult
	if err := tx.QueryRow(ctx,
		`SELECT input_log, checksums, result FROM match_submissions WHERE match_id = $1`, id).
		Scan(&s.inputLog, &s.checksums, &stored); err != nil {
		log.Printf("unilateral: reading the submission for %d: %v", id, err)
		return false
	}
	stored.into(&s)

	// **Accepted as a disconnect whatever the client called it.** One report and
	// one silence is what a disconnect looks like from here, and the client that
	// never spoke is the one that stopped playing.
	s.disconnect = true

	if err := v.matches.settleTx(ctx, tx, row, s); err != nil {
		log.Printf("unilateral: settling %d: %v", id, err)
		return false
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("unilateral: committing %d: %v", id, err)
		return false
	}
	log.Printf("match %d settled on one report: seat %d won", id, s.winner)
	return true
}

// runUnilateral drains the sweep on a slow tick until ctx ends.
func (v verifier) runUnilateral(ctx context.Context) {
	t := time.NewTicker(unilateralPoll)
	defer t.Stop()
	for {
		for v.sweepUnilateral(ctx) {
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
