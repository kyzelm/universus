// Match verification by re-simulation
// (02 Architecture/Anti-Cheat and Verification.md).
//
// **This does not prevent cheating. It detects it after the fact.** In a
// peer-to-peer architecture there is no authority during the match: both
// clients hold full simulation state and full control of their own machine.
// What is achievable is replaying the match here and checking that the reported
// outcome is the one the rules actually produce.
//
// **It is the single strongest justification for the Go→WASM decision** (D6).
// One simulation codebase, compiled to WASM for the client and native for this
// binary — without shared code the re-simulation would be a reimplementation,
// and a reimplementation that must agree bit for bit is a far worse problem
// than the one being solved.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"universus/sim"
	"universus/sim/replaylog"
)

const (
	// Re-simulating a three-round match takes tens of milliseconds — the sim
	// runs far faster than real time with nothing to render — so a couple of
	// workers is ample for single-digit concurrency and leaves the request path
	// alone.
	verifyWorkers = 2

	// How often an idle worker looks for work. LISTEN/NOTIFY would make this
	// instant and would be a second mechanism for a queue that is empty most of
	// the time; verification is not something anybody waits on.
	verifyPoll = 3 * time.Second

	// A job that keeps failing is a bug in this code or a log this build cannot
	// read, and either way retrying forever hides it.
	maxAttempts = 3
)

// verdict is what a re-simulation concluded.
type verdict struct {
	ok     bool
	reason string
	// Where the checksums first diverged, for the report. -1 when they did not.
	frame int
	// What check 3 made of the input stream. Never affects `ok`: a plausibility
	// anomaly is a flag for review and never a verdict on the match.
	anomalies []anomaly
}

// verifier drains verification_jobs.
type verifier struct {
	matches matchStore
	// The data version this binary was built with. A log recorded against
	// different frame data replays into a different match through no fault of
	// the player, which is a "cannot verify" rather than a "you cheated".
	dataVersion uint32
}

// run starts the pool and returns when ctx ends.
func (v verifier) run(ctx context.Context, workers int) {
	for i := 0; i < workers; i++ {
		go func() {
			t := time.NewTicker(verifyPoll)
			defer t.Stop()
			for {
				// Drain rather than one-per-tick: a burst of matches ending
				// together should not take a tick each.
				for v.step(ctx) {
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
		}()
	}
}

// step claims one job, verifies it, and reports whether it found any work.
//
// **The claim, the re-simulation and the outcome are one transaction.** A
// worker that died between deciding and writing would leave the job claimed and
// the match unjudged; here it leaves nothing at all and the next poll picks the
// job up again.
func (v verifier) step(ctx context.Context) bool {
	tx, err := v.matches.pool.Begin(ctx)
	if err != nil {
		log.Printf("verify: %v", err)
		return false
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// FOR UPDATE SKIP LOCKED is the whole of the queue: two workers asking at
	// once get different rows rather than one waiting on the other, and no
	// message broker is involved (02 Architecture/Backend Services.md).
	var matchID int64
	var attempts int
	err = tx.QueryRow(ctx,
		`SELECT match_id, attempts FROM verification_jobs
		 WHERE status = 'queued' ORDER BY queued_at
		 FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&matchID, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		log.Printf("verify: claiming a job: %v", err)
		return false
	}

	result, err := v.judge(ctx, tx, matchID)
	if err != nil {
		// Something went wrong *here*, which is not a verdict about the match.
		status := "queued"
		if attempts+1 >= maxAttempts {
			status = "failed"
			log.Printf("verify: match %d failed %d times, giving up: %v", matchID, attempts+1, err)
		}
		if _, e := tx.Exec(ctx,
			`UPDATE verification_jobs SET status = $2, attempts = attempts + 1,
			        result = $3 WHERE match_id = $1`,
			matchID, status, map[string]any{"error": err.Error()}); e != nil {
			log.Printf("verify: recording a failure: %v", e)
			return false
		}
		_ = tx.Commit(ctx)
		return true
	}

	if _, err := tx.Exec(ctx,
		`UPDATE verification_jobs SET status = 'done', attempts = attempts + 1,
		        result = $2 WHERE match_id = $1`,
		matchID, map[string]any{
			"ok": result.ok, "reason": result.reason, "frame": result.frame,
			// Stored whether or not anything tripped, because **the signal is
			// across matches rather than inside one**: an account that reads
			// "median reaction 11 frames over 12 observations" once is noise
			// and the same account forty times is not, and only the kept
			// numbers can tell those apart.
			"anomalies": result.anomalies,
		}); err != nil {
		log.Printf("verify: recording match %d: %v", matchID, err)
		return false
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("verify: committing match %d: %v", matchID, err)
	}
	return true
}

// judge re-simulates the match and applies the consequence.
func (v verifier) judge(ctx context.Context, tx pgx.Tx, matchID int64) (verdict, error) {
	var row matchRow
	var winner *int
	var endFrame *int
	var p1Change, p2Change *int
	var disconnected bool
	err := tx.QueryRow(ctx,
		`SELECT id, mode, p1_id, p2_id, winner, end_frame, p1_lp_change, p2_lp_change, disconnected
		 FROM matches WHERE id = $1 FOR UPDATE`, matchID).
		Scan(&row.id, &row.mode, &row.p1, &row.p2, &winner, &endFrame, &p1Change, &p2Change,
			&disconnected)
	if err != nil {
		return verdict{}, err
	}
	if winner == nil {
		return verdict{}, errors.New("the match has no reported winner")
	}

	var inputLog, checksums []byte
	// Either submission will do: they were compared byte for byte before the
	// match was settled, so agreeing is the reason this row exists at all.
	err = tx.QueryRow(ctx,
		`SELECT input_log, checksums FROM match_submissions
		 WHERE match_id = $1 ORDER BY user_id LIMIT 1`, matchID).Scan(&inputLog, &checksums)
	if err != nil {
		return verdict{}, fmt.Errorf("reading the submission: %w", err)
	}

	result := v.resimulate(inputLog, checksums, *winner, *endFrame, disconnected)
	if result.ok {
		if _, err := tx.Exec(ctx, `UPDATE matches SET verified = 'ok' WHERE id = $1`, matchID); err != nil {
			return verdict{}, err
		}
		// **Check 3 runs on a match that passed, and changes nothing about it.**
		// An assist bot does not lie about the match — it plays a real one with
		// inhuman execution, so every hash it reports is correct and checks 1
		// and 2 have nothing to say. The result stands, the account is flagged,
		// and a person decides.
		result.anomalies = v.flagImplausible(ctx, tx, row, inputLog)
		return result, nil
	}

	// **The correction.** LP was paid on cross-client agreement (D109) and this
	// is where it is taken back: the match is void, the exact amounts that were
	// paid are subtracted, and both accounts are flagged for a person to read.
	// Flagged and not banned — a re-simulation mismatch is also what a bug in
	// this very code looks like.
	if _, err := tx.Exec(ctx,
		`UPDATE matches SET verified = 'mismatch' WHERE id = $1`, matchID); err != nil {
		return verdict{}, err
	}
	if err := reverseLadder(ctx, tx, row, p1Change, p2Change); err != nil {
		return verdict{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET flagged = true WHERE id IN ($1, $2)`, row.p1, row.p2); err != nil {
		return verdict{}, err
	}
	return result, nil
}

// resimulate runs the log through the native sim and compares.
//
// The comparison is the checkpoint checksums, **not the final state**. A match
// whose end state happens to match after diverging in the middle is still a
// match that did not happen, and the frame the hashes first disagree on is the
// only diagnostic worth having.
// disconnected changes what can be checked, and the difference is worth being
// precise about. A match that ended because somebody stopped sending has a log
// that **legitimately does not reach a match end**: the winner was decided by
// policy (D50) rather than by the simulation, so asking the replay who won
// would fail every honest disconnect.
//
// What still holds is that the log replays to its own hashes. That catches a
// tampered log — somebody editing an honest match's inputs — and it does not
// catch a fabricated one, because a client that made up the whole match made up
// hashes to match it. **That is the exploit D52 accepts and the thesis states**:
// with only one report there is no second copy to compare against, and what is
// left is that a forgery has to be a plausible simulation rather than any
// outcome at all.
func (v verifier) resimulate(inputLog, checksums []byte, winner, endFrame int, disconnected bool) verdict {
	parsed, err := replaylog.Decode("the uploaded log", inputLog)
	if err != nil {
		return verdict{reason: err.Error(), frame: -1}
	}
	if parsed.DataVersion != v.dataVersion {
		// Not a verdict about the players: their client was built against
		// different frame data, so it played a different game to the one this
		// binary can replay.
		return verdict{
			reason: fmt.Sprintf("recorded against data version %08x, this build is %08x",
				parsed.DataVersion, v.dataVersion),
			frame: -1,
		}
	}

	s := sim.NewSessionOf(parsed.Setup)
	want := len(checksums) / 4
	taken := 0
	for _, in := range parsed.Inputs {
		s.Advance(in)
		if s.Frame()%checksumEvery != 0 {
			continue
		}
		if taken >= want {
			// More checkpoints than were uploaded: the client stopped early, or
			// sent somebody else's series.
			return verdict{reason: "more checkpoints than the client uploaded", frame: int(s.Frame())}
		}
		theirs := binary.LittleEndian.Uint32(checksums[taken*4:])
		if s.Checksum() != theirs {
			return verdict{
				reason: fmt.Sprintf("checksum %08x, the client reported %08x", s.Checksum(), theirs),
				frame:  int(s.Frame()),
			}
		}
		taken++
	}

	if taken != want {
		return verdict{
			reason: fmt.Sprintf("%d checkpoints replayed against %d uploaded", taken, want),
			frame:  -1,
		}
	}
	if got := int(s.Frame()); got != endFrame {
		return verdict{reason: fmt.Sprintf("ended on frame %d, reported %d", got, endFrame), frame: got}
	}
	if disconnected {
		return verdict{ok: true, frame: -1}
	}

	// The sim counts seats from 0 and reports -1 while a match is undecided;
	// the server counts them from 1. A match that never ended is a report about
	// a match that is still being played.
	if s.State().Winner == sim.RoundNobody {
		return verdict{reason: "the log ends before the match does", frame: int(s.Frame())}
	}
	if got := int(s.State().Winner) + 1; got != winner {
		return verdict{reason: fmt.Sprintf("seat %d won, reported %d", got, winner), frame: int(s.Frame())}
	}
	return verdict{ok: true, frame: -1}
}

// The client takes a checkpoint every this many frames — CHECKSUM_EVERY in
// client/src/net/netplay.ts, pinned by a test there that names this constant.
//
// **This is the one coupling the determinism gate does not cover.** The gate
// proves the native and WASM sims agree on the hash at every frame; it says
// nothing about which frames the client chose to sample. Drift here and every
// ranked match fails verification with a checkpoint-count mismatch, which is at
// least loud — but it is loud in production rather than in CI, hence the test.
const checksumEvery = 30

// flagImplausible runs check 3 and flags the accounts it names.
//
// Flag, never ban, and never a penalty: the checks are probabilistic, a short
// match is a small sample, and a player with genuinely fast hands trips the
// same threshold a bot does. What this buys is a queue for a person to look at.
func (v verifier) flagImplausible(ctx context.Context, tx pgx.Tx, row matchRow, inputLog []byte) []anomaly {
	parsed, err := replaylog.Decode("the uploaded log", inputLog)
	if err != nil {
		return nil // already replayed once by here, so this cannot happen
	}

	found := plausibility(parsed)
	for _, a := range found {
		who := row.p1
		if a.Seat == 2 {
			who = row.p2
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET flagged = true WHERE id = $1`, who); err != nil {
			log.Printf("verify: flagging %d: %v", who, err)
		}
		log.Printf("match %d: seat %d flagged for review — %s", row.id, a.Seat, a.Note)
	}
	return found
}

// reverseLadder subtracts exactly what was paid.
//
// Recomputing is not an option: lpChange is a function of both ratings at the
// time and they have moved since, and the loser's side is not the winner's
// negated because a loss is floored at a tier boundary. Both amounts were
// recorded when the match settled (migration 003).
func reverseLadder(ctx context.Context, tx pgx.Tx, row matchRow, p1Change, p2Change *int) error {
	if p1Change == nil || p2Change == nil {
		return nil // casual, or a match that paid nothing
	}

	for _, u := range []struct {
		id     int64
		change int
	}{{row.p1, *p1Change}, {row.p2, *p2Change}} {
		var lp int
		if err := tx.QueryRow(ctx,
			`SELECT lp FROM ratings WHERE user_id = $1 FOR UPDATE`, u.id).Scan(&lp); err != nil {
			return err
		}
		// Clamped at zero, which is the one case where the reversal cannot be
		// exact: a player who lost to the floor and then lost again has fewer
		// points to give back than were taken. Rare, bounded, and the honest
		// alternative is a negative rating.
		lp = max(lp-u.change, 0)

		won := 0
		if (u.id == row.p1) == (*p1Change > 0) {
			won = 1
		}
		if _, err := tx.Exec(ctx,
			`UPDATE ratings SET lp = $2, tier = $3, matches = greatest(matches - 1, 0),
			        wins = greatest(wins - $4, 0), updated_at = now() WHERE user_id = $1`,
			u.id, lp, tierOf(lp), won); err != nil {
			return err
		}
	}
	return nil
}
