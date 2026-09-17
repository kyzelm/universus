// Match results and what they do to the ladder
// (02 Architecture/Anti-Cheat and Verification.md, 03 Game Design/Game Modes.md).
//
//	POST /api/match/{id}/result
//
// **Both clients upload independently**, and the server compares what they sent
// before it believes either of them. That comparison is check 1 of three: the
// two input logs are the same bytes or at least one client is lying. Check 2 is
// re-simulation and check 3 is input plausibility; both are their own task, and
// this endpoint queues the job that will run them.
//
// There is no authority during a peer-to-peer match and this does not pretend
// otherwise. It detects after the fact.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A match is under 20 KB packed (02 Architecture/WASM Boundary.md). The cap is
// an order of magnitude above that: generous for a long match, mean for a
// client that has decided to send us its hard drive.
const maxUpload int64 = 256 << 10

type result struct {
	// 1 or 2 — the seat, not the account. The server knows which account sat
	// in which seat and does not need the client to tell it twice.
	Winner   int    `json:"winner"`
	EndFrame int    `json:"endFrame"`
	Chars    [2]int `json:"chars"`
	// The replay log, header and all: exactly the bytes tools/replay reads and
	// exactly the bytes the re-simulation will consume.
	InputLog  string `json:"inputLog"`
	Checksums string `json:"checksums"`
}

// submission is one client's upload as stored.
type submission struct {
	winner    int
	endFrame  int
	chars     [2]int
	inputLog  []byte
	checksums []byte
}

// matchRow is what the server knows about a match before anybody reports on it.
type matchRow struct {
	id     int64
	mode   string
	p1, p2 int64
	winner *int
}

type matchStore struct{ pool *pgxpool.Pool }

// create records a pairing. Called when the queue introduces two players, which
// is before either has chosen a character — see migration 002 for why those
// columns are nullable rather than zero.
func (m matchStore) create(ctx context.Context, mode string, p1, p2 int64) (int64, error) {
	var id int64
	err := m.pool.QueryRow(ctx,
		`INSERT INTO matches (mode, p1_id, p2_id) VALUES ($1, $2, $3) RETURNING id`,
		mode, p1, p2).Scan(&id)
	return id, err
}

func (m matchStore) byID(ctx context.Context, id int64) (matchRow, error) {
	var r matchRow
	err := m.pool.QueryRow(ctx,
		`SELECT id, mode, p1_id, p2_id, winner FROM matches WHERE id = $1`, id).
		Scan(&r.id, &r.mode, &r.p1, &r.p2, &r.winner)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, errNoMatch
	}
	return r, err
}

var (
	errNoMatch     = errors.New("no such match")
	errNotYours    = errors.New("not a player in that match")
	errAlreadySent = errors.New("already submitted")
	errSettled     = errors.New("that match is already settled")
)

// record stores one player's upload and returns the other player's, or nil when
// theirs has not arrived yet.
func (m matchStore) record(ctx context.Context, id, user int64, s submission) (*submission, error) {
	_, err := m.pool.Exec(ctx,
		`INSERT INTO match_submissions (match_id, user_id, input_log, checksums, result)
		 VALUES ($1, $2, $3, $4, $5)`,
		id, user, s.inputLog, s.checksums,
		map[string]any{"winner": s.winner, "endFrame": s.endFrame, "chars": s.chars})
	if uniqueViolation(err) {
		return nil, errAlreadySent
	}
	if err != nil {
		return nil, err
	}

	var other submission
	var res struct {
		Winner   int    `json:"winner"`
		EndFrame int    `json:"endFrame"`
		Chars    [2]int `json:"chars"`
	}
	err = m.pool.QueryRow(ctx,
		`SELECT input_log, checksums, result FROM match_submissions
		 WHERE match_id = $1 AND user_id <> $2`, id, user).
		Scan(&other.inputLog, &other.checksums, &res)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // theirs has not arrived
	}
	if err != nil {
		return nil, err
	}
	other.winner, other.endFrame, other.chars = res.Winner, res.EndFrame, res.Chars
	return &other, nil
}

// settle writes the agreed result, applies the ladder, and queues verification.
//
// **One transaction.** A match that counted for one player and not the other is
// the worst outcome available here, and it is the one a partial failure gives.
func (m matchStore) settle(ctx context.Context, r matchRow, s submission) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Casual is accepted as reported, which is what "no verification" means —
	// and it is what makes ranked and casual genuinely different systems rather
	// than two buttons on a menu (D19).
	verified := "ok"
	if r.mode == "ranked" {
		verified = "pending"
	}

	if _, err := tx.Exec(ctx,
		`UPDATE matches SET winner = $2, end_frame = $3, p1_character = $4, p2_character = $5,
		        verified = $6 WHERE id = $1`,
		r.id, s.winner, s.endFrame, s.chars[0], s.chars[1], verified); err != nil {
		return err
	}

	if r.mode == "ranked" {
		if err := applyLadder(ctx, tx, r, s.winner); err != nil {
			return err
		}
		// The job the re-simulation will pick up. Queued here rather than run
		// here: re-simulating a match takes tens of milliseconds and belongs in
		// a worker, not in the request the player is waiting on.
		if _, err := tx.Exec(ctx,
			`INSERT INTO verification_jobs (match_id) VALUES ($1) ON CONFLICT DO NOTHING`,
			r.id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// applyLadder moves both ratings.
//
// **LP is applied on cross-client agreement, not after re-simulation**, and the
// verification job may take it back. The alternative is a player finishing a
// ranked match and seeing nothing happen until a worker gets to them, which
// reads as a broken ladder — every game in the genre pays provisionally for the
// same reason. What verification buys is the correction, not the delay.
func applyLadder(ctx context.Context, tx pgx.Tx, r matchRow, winnerSeat int) error {
	winner, loser := r.p1, r.p2
	if winnerSeat == 2 {
		winner, loser = r.p2, r.p1
	}

	// Locked in a fixed order — lowest id first — because two matches settling
	// at once on overlapping players is exactly how a deadlock is written.
	first, second := winner, loser
	if second < first {
		first, second = second, first
	}
	lp := map[int64]int{}
	rows, err := tx.Query(ctx,
		`SELECT user_id, lp FROM ratings WHERE user_id IN ($1, $2) ORDER BY user_id FOR UPDATE`,
		first, second)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, points int
		if err := rows.Scan(&id, &points); err != nil {
			rows.Close()
			return err
		}
		lp[int64(id)] = points
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	change := lpChange(lp[winner], lp[loser])
	newWinner := lp[winner] + change
	newLoser := applyLoss(lp[loser], change)

	for _, u := range []struct {
		id   int64
		lp   int
		won  int
		name string
	}{
		{winner, newWinner, 1, "winner"},
		{loser, newLoser, 0, "loser"},
	} {
		if _, err := tx.Exec(ctx,
			`UPDATE ratings SET lp = $2, tier = $3, matches = matches + 1, wins = wins + $4,
			        updated_at = now() WHERE user_id = $1`,
			u.id, u.lp, tierOf(u.lp), u.won); err != nil {
			return err
		}
	}
	return nil
}

// void is what a disagreement costs: no winner, no LP, and both accounts
// flagged for a person to look at. **Flagged, never banned** — check 1 catches
// a liar and a missed desync equally well, and it cannot tell them apart.
func (m matchStore) void(ctx context.Context, r matchRow) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `UPDATE matches SET verified = 'mismatch' WHERE id = $1`, r.id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET flagged = true WHERE id IN ($1, $2)`, r.p1, r.p2); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *auth) submitResult(w http.ResponseWriter, r *http.Request, u User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "that is not a match id")
		return
	}

	var in result
	if !readBigJSON(w, r, &in) {
		return
	}
	s, err := in.decode()
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	match, err := a.matches.byID(ctx, id)
	if err != nil {
		// A match that does not exist and one somebody else played are the same
		// answer: otherwise this endpoint counts how many matches have been
		// played, one probe at a time.
		httpError(w, http.StatusNotFound, "no such match")
		return
	}
	if u.ID != match.p1 && u.ID != match.p2 {
		httpError(w, http.StatusNotFound, "no such match")
		return
	}
	if match.winner != nil {
		httpError(w, http.StatusConflict, "that match is already settled")
		return
	}

	other, err := a.matches.record(ctx, id, u.ID, s)
	if errors.Is(err, errAlreadySent) {
		httpError(w, http.StatusConflict, "you have already uploaded this match")
		return
	}
	if err != nil {
		log.Printf("recording match %d: %v", id, err)
		httpError(w, http.StatusInternalServerError, "could not store the result")
		return
	}

	if other == nil {
		// One upload is not a result. It is also what a disconnect looks like,
		// which is why the timeout that accepts one provisionally belongs with
		// disconnect handling rather than here.
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "waiting for the other player"})
		return
	}

	if !agree(s, *other) {
		if err := a.matches.void(ctx, match); err != nil {
			log.Printf("voiding match %d: %v", id, err)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "mismatch"})
		return
	}

	if err := a.matches.settle(ctx, match, s); err != nil {
		log.Printf("settling match %d: %v", id, err)
		httpError(w, http.StatusInternalServerError, "could not settle the match")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "settled"})
}

// agree is check 1: **do the two uploaded input logs match?** A disagreement
// means at least one client lied, or a desync went undetected — and either way
// the match is void, because nothing here can tell those two apart.
func agree(a, b submission) bool {
	return a.winner == b.winner && a.endFrame == b.endFrame && a.chars == b.chars &&
		bytes.Equal(a.inputLog, b.inputLog) && bytes.Equal(a.checksums, b.checksums)
}

func (in result) decode() (submission, error) {
	if in.Winner != 1 && in.Winner != 2 {
		return submission{}, errors.New("winner must be 1 or 2")
	}
	if in.EndFrame <= 0 {
		return submission{}, errors.New("endFrame must be positive")
	}
	inputLog, err := base64.StdEncoding.DecodeString(in.InputLog)
	if err != nil || len(inputLog) == 0 {
		return submission{}, errors.New("inputLog must be base64 and not empty")
	}
	checksums, err := base64.StdEncoding.DecodeString(in.Checksums)
	if err != nil || len(checksums)%4 != 0 {
		return submission{}, errors.New("checksums must be base64 uint32s")
	}
	return submission{
		winner: in.Winner, endFrame: in.EndFrame, chars: in.Chars,
		inputLog: inputLog, checksums: checksums,
	}, nil
}
