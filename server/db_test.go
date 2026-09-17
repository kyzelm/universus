package main

import (
	"context"
	"encoding/binary"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"universus/sim"
	"universus/sim/replaylog"
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

// --- match results and the ladder ------------------------------------------
//
// Tested against a real Postgres rather than a fake, because what is under test
// *is* the SQL: two transactions, a unique index doing the "already submitted"
// check, and a rating that has to move for exactly two accounts.

func twoPlayers(t *testing.T, ctx context.Context, p *pgxpool.Pool) (pgStore, matchStore, User, User) {
	t.Helper()
	if _, err := migrate(ctx, p, migrationFiles); err != nil {
		t.Fatal(err)
	}
	users, matches := pgStore{pool: p}, matchStore{pool: p}

	a, err := users.createUser(ctx, "a@example.com", "x", "player-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := users.createUser(ctx, "b@example.com", "x", "player-b")
	if err != nil {
		t.Fatal(err)
	}
	return users, matches, a, b
}

func upload(winner int) submission {
	return submission{
		winner: winner, endFrame: 3600, chars: [2]int{0, 1},
		inputLog:  []byte("UNIV\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00 inputs"),
		checksums: []byte{1, 0, 0, 0, 2, 0, 0, 0},
	}
}

func TestARankedResultMovesBothRatings(t *testing.T) {
	ctx, p := testPool(t)
	users, matches, a, b := twoPlayers(t, ctx, p)

	id, err := matches.create(ctx, "ranked", a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}

	// One upload is not a result.
	other, err := matches.record(ctx, id, a.ID, upload(1))
	if err != nil || other != nil {
		t.Fatalf("first upload: other=%v err=%v", other, err)
	}
	// And the same player cannot upload twice.
	if _, err := matches.record(ctx, id, a.ID, upload(1)); err != errAlreadySent {
		t.Errorf("a second upload from the same player gave %v", err)
	}

	other, err = matches.record(ctx, id, b.ID, upload(1))
	if err != nil || other == nil {
		t.Fatalf("second upload: other=%v err=%v", other, err)
	}
	if !agree(upload(1), *other) {
		t.Fatal("two identical uploads did not agree")
	}

	row, err := matches.byID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := matches.settle(ctx, row, upload(1)); err != nil {
		t.Fatal(err)
	}

	// Seat 1 won, so player a gained and player b lost the same amount.
	ra, rb := users.rating(ctx, a.ID), users.rating(ctx, b.ID)
	if ra.LP != lpBase || rb.LP != 0 {
		t.Errorf("ratings are %d and %d, want %d and 0 (floored at the bottom tier)", ra.LP, rb.LP, lpBase)
	}
	if ra.Matches != 1 || ra.Wins != 1 || rb.Matches != 1 || rb.Wins != 0 {
		t.Errorf("records are %+v and %+v", ra, rb)
	}

	// Ranked queues a verification job; the result is provisional until it runs.
	var status string
	if err := p.QueryRow(ctx, `SELECT status FROM verification_jobs WHERE match_id = $1`, id).
		Scan(&status); err != nil {
		t.Fatalf("no verification job: %v", err)
	}
	var verified string
	if err := p.QueryRow(ctx, `SELECT verified FROM matches WHERE id = $1`, id).Scan(&verified); err != nil {
		t.Fatal(err)
	}
	if verified != "pending" {
		t.Errorf("a ranked match is %q, want pending until it is re-simulated", verified)
	}
}

// Casual is accepted as reported and changes nothing, which is what makes the
// two modes different systems rather than two buttons (D19).
func TestACasualResultChangesNoRating(t *testing.T) {
	ctx, p := testPool(t)
	users, matches, a, b := twoPlayers(t, ctx, p)

	id, _ := matches.create(ctx, "casual", a.ID, b.ID)
	matches.record(ctx, id, a.ID, upload(1))
	matches.record(ctx, id, b.ID, upload(1))

	row, _ := matches.byID(ctx, id)
	if err := matches.settle(ctx, row, upload(1)); err != nil {
		t.Fatal(err)
	}

	if ra := users.rating(ctx, a.ID); ra.LP != 0 || ra.Matches != 0 {
		t.Errorf("a casual win moved the ladder: %+v", ra)
	}

	var jobs int
	p.QueryRow(ctx, `SELECT count(*) FROM verification_jobs WHERE match_id = $1`, id).Scan(&jobs)
	if jobs != 0 {
		t.Errorf("a casual match queued %d verification job(s)", jobs)
	}

	var verified string
	p.QueryRow(ctx, `SELECT verified FROM matches WHERE id = $1`, id).Scan(&verified)
	if verified != "ok" {
		t.Errorf("a casual match is %q, want ok", verified)
	}
}

// **Two clients that disagree void the match and flag both accounts.** Check 1
// cannot tell a liar from a desync that went undetected, which is exactly why
// the consequence is a flag for a person to look at rather than a ban.
func TestADisagreementVoidsTheMatchAndFlagsBoth(t *testing.T) {
	ctx, p := testPool(t)
	users, matches, a, b := twoPlayers(t, ctx, p)

	id, _ := matches.create(ctx, "ranked", a.ID, b.ID)
	matches.record(ctx, id, a.ID, upload(1))
	other, err := matches.record(ctx, id, b.ID, upload(2)) // the other player "won"
	if err != nil {
		t.Fatal(err)
	}
	// `other` is what the *first* player sent, so the comparison is this
	// upload against theirs — which is exactly the comparison the handler makes.
	if agree(upload(2), *other) {
		t.Fatal("two different winners agreed")
	}
	if other.winner != 1 {
		t.Errorf("the stored result came back with winner %d, want 1", other.winner)
	}

	row, _ := matches.byID(ctx, id)
	if err := matches.void(ctx, row); err != nil {
		t.Fatal(err)
	}

	var verified string
	var winner *int
	p.QueryRow(ctx, `SELECT verified, winner FROM matches WHERE id = $1`, id).Scan(&verified, &winner)
	if verified != "mismatch" || winner != nil {
		t.Errorf("a voided match is %q with winner %v", verified, winner)
	}
	if ra := users.rating(ctx, a.ID); ra.LP != 0 || ra.Matches != 0 {
		t.Errorf("a voided match paid LP: %+v", ra)
	}

	var flagged int
	p.QueryRow(ctx, `SELECT count(*) FROM users WHERE flagged`).Scan(&flagged)
	if flagged != 2 {
		t.Errorf("%d accounts flagged, want both", flagged)
	}
}

// A win at the bottom of a tier must not demote the loser out of it, and the
// arithmetic has to survive the round trip through the database.
func TestTheTierFloorHoldsThroughTheDatabase(t *testing.T) {
	ctx, p := testPool(t)
	users, matches, a, b := twoPlayers(t, ctx, p)

	// Both a point into Sophomore.
	if _, err := p.Exec(ctx, `UPDATE ratings SET lp = 1005, tier = 1`); err != nil {
		t.Fatal(err)
	}

	id, _ := matches.create(ctx, "ranked", a.ID, b.ID)
	matches.record(ctx, id, a.ID, upload(1))
	matches.record(ctx, id, b.ID, upload(1))
	row, _ := matches.byID(ctx, id)
	if err := matches.settle(ctx, row, upload(1)); err != nil {
		t.Fatal(err)
	}

	loser := users.rating(ctx, b.ID)
	if loser.LP != 1000 || loser.Tier != 1 {
		t.Errorf("the loser is %d LP in tier %d (%s), want 1000 and Sophomore",
			loser.LP, loser.Tier, loser.TierName)
	}
	if winner := users.rating(ctx, a.ID); winner.LP != 1005+lpBase {
		t.Errorf("the winner is %d LP, want %d", winner.LP, 1005+lpBase)
	}
}

// --- verification -----------------------------------------------------------

// settled plays a real match, uploads it from both ends, and settles it — the
// state a verification job is created in.
func settled(t *testing.T, ctx context.Context, p *pgxpool.Pool, tamper func(s *submission)) (
	pgStore, matchStore, User, User, int64,
) {
	t.Helper()
	users, matches, a, b := twoPlayers(t, ctx, p)

	logBytes, checksums, winner, endFrame := playedMatch(t)
	s := submission{
		winner: winner, endFrame: endFrame, chars: [2]int{0, 1},
		inputLog: logBytes, checksums: checksums,
	}
	if tamper != nil {
		tamper(&s)
	}

	id, err := matches.create(ctx, "ranked", a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Both ends upload the same thing, which is what got the match settled in
	// the first place — the lie this catches is one both clients agree on.
	if _, err := matches.record(ctx, id, a.ID, s); err != nil {
		t.Fatal(err)
	}
	if _, err := matches.record(ctx, id, b.ID, s); err != nil {
		t.Fatal(err)
	}
	row, err := matches.byID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := matches.settle(ctx, row, s); err != nil {
		t.Fatal(err)
	}
	return users, matches, a, b, id
}

func jobResult(t *testing.T, ctx context.Context, p *pgxpool.Pool, id int64) (string, string) {
	t.Helper()
	var status, verified string
	if err := p.QueryRow(ctx, `SELECT status FROM verification_jobs WHERE match_id = $1`, id).
		Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `SELECT verified FROM matches WHERE id = $1`, id).
		Scan(&verified); err != nil {
		t.Fatal(err)
	}
	return status, verified
}

func TestAnHonestMatchSurvivesVerification(t *testing.T) {
	ctx, p := testPool(t)
	users, matches, a, b, id := settled(t, ctx, p, nil)

	before := []Rating{users.rating(ctx, a.ID), users.rating(ctx, b.ID)}
	v := verifier{matches: matches, dataVersion: loadRoster()}
	if !v.step(ctx) {
		t.Fatal("the worker found no job")
	}

	if status, verified := jobResult(t, ctx, p, id); status != "done" || verified != "ok" {
		t.Errorf("job %q, match %q, want done and ok", status, verified)
	}
	for i, u := range []User{a, b} {
		if got := users.rating(ctx, u.ID); got != before[i] {
			t.Errorf("player %d: %+v became %+v", i+1, before[i], got)
		}
	}

	// And the queue is empty afterwards, which is what says the job was claimed
	// rather than left for the next poll to find again.
	if v.step(ctx) {
		t.Error("the same job was claimed twice")
	}
}

// **The correction.** LP was paid on cross-client agreement (D109); this is
// where a match that could not be replayed takes it back.
func TestAMatchThatFailsResimulationIsReversed(t *testing.T) {
	ctx, p := testPool(t)
	// Both clients upload the same tampered checksum series, which is what
	// collusion or a shared modified build looks like from here.
	users, matches, a, b, id := settled(t, ctx, p, func(s *submission) {
		s.checksums[8] ^= 0xff
	})

	paid := users.rating(ctx, a.ID)
	if paid.LP == 0 {
		t.Fatal("the winner was never paid, so there is nothing to reverse")
	}

	v := verifier{matches: matches, dataVersion: loadRoster()}
	if !v.step(ctx) {
		t.Fatal("the worker found no job")
	}

	if status, verified := jobResult(t, ctx, p, id); status != "done" || verified != "mismatch" {
		t.Errorf("job %q, match %q, want done and mismatch", status, verified)
	}

	for i, u := range []User{a, b} {
		got := users.rating(ctx, u.ID)
		if got.LP != 0 || got.Matches != 0 || got.Wins != 0 {
			t.Errorf("player %d is %+v, want the rating it started with", i+1, got)
		}
	}

	var flagged int
	p.QueryRow(ctx, `SELECT count(*) FROM users WHERE flagged`).Scan(&flagged)
	if flagged != 2 {
		t.Errorf("%d accounts flagged, want both", flagged)
	}
}

// Two workers asking at once get different rows rather than one waiting on the
// other — the whole of what SKIP LOCKED is doing here.
func TestTwoWorkersDoNotClaimTheSameJob(t *testing.T) {
	ctx, p := testPool(t)
	_, matches, _, _, _ := settled(t, ctx, p, nil)

	v := verifier{matches: matches, dataVersion: loadRoster()}

	var wg sync.WaitGroup
	claimed := make([]bool, 4)
	for i := range claimed {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed[i] = v.step(ctx)
		}()
	}
	wg.Wait()

	got := 0
	for _, c := range claimed {
		if c {
			got++
		}
	}
	if got != 1 {
		t.Errorf("%d workers claimed the one job", got)
	}
}

// --- disconnects ------------------------------------------------------------

// **The grace period is the mechanism.** Without it, whichever client's packet
// arrived first would decide the match, and a quitter fabricating a counter
// claim would be racing the player they quit on.
func TestALoneSubmissionWaitsBeforeItCounts(t *testing.T) {
	ctx, p := testPool(t)
	users, matches, a, b := twoPlayers(t, ctx, p)

	logBytes, checksums, winner, endFrame := playedMatch(t)
	s := submission{
		winner: winner, endFrame: endFrame, chars: [2]int{0, 1},
		disconnect: true, transport: "relay", rttMs: 84, rollback: 1.75,
		inputLog: logBytes, checksums: checksums,
	}

	id, err := matches.create(ctx, "ranked", a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := matches.record(ctx, id, a.ID, s); err != nil {
		t.Fatal(err)
	}

	v := verifier{matches: matches, dataVersion: loadRoster()}
	if v.sweepUnilateral(ctx) {
		t.Fatal("a submission seconds old was accepted on its own")
	}

	// Backdated rather than waited out: the rule is about elapsed time, and a
	// test that spends thirty seconds proving it is a test nobody runs.
	if _, err := p.Exec(ctx,
		`UPDATE match_submissions SET received_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	if !v.sweepUnilateral(ctx) {
		t.Fatal("a submission an hour old was still waiting")
	}

	// The survivor's claim stands: full win for them, full loss for the player
	// who stopped reporting.
	if ra := users.rating(ctx, a.ID); ra.LP != lpBase || ra.Wins != 1 {
		t.Errorf("the surviving player is %+v", ra)
	}

	var disconnected bool
	var transport string
	var rtt int
	var rollback float64
	if err := p.QueryRow(ctx,
		`SELECT disconnected, transport, avg_rtt_ms, rollback_avg FROM matches WHERE id = $1`, id).
		Scan(&disconnected, &transport, &rtt, &rollback); err != nil {
		t.Fatal(err)
	}
	if !disconnected {
		t.Error("the match is not marked as a disconnect")
	}
	// The conditions are recorded because the P2P-versus-relay figure is one
	// that only exists if the match that produced it says which it was.
	if transport != "relay" || rtt != 84 || rollback != 1.75 {
		t.Errorf("conditions recorded as %q, %d ms, %.2f", transport, rtt, rollback)
	}

	// The counts the policy actually rests on, and neither is a punishment.
	var disconnects, unilateral int
	p.QueryRow(ctx, `SELECT disconnects FROM ratings WHERE user_id = $1`, b.ID).Scan(&disconnects)
	p.QueryRow(ctx, `SELECT unilateral_wins FROM ratings WHERE user_id = $1`, a.ID).Scan(&unilateral)
	if disconnects != 1 || unilateral != 1 {
		t.Errorf("%d disconnects for the player who left, %d unilateral wins for the survivor",
			disconnects, unilateral)
	}

	// And it is still verified: a fabricated log has to be a valid simulation
	// that produces the claimed result, which is the whole constraint on a
	// unilateral claim.
	if !v.step(ctx) {
		t.Fatal("a unilateral settle queued no verification job")
	}
	if _, verified := jobResult(t, ctx, p, id); verified != "ok" {
		t.Errorf("the match verified as %q", verified)
	}
}

// The second player arriving in time is the ordinary path, and the sweep must
// not settle a match twice.
func TestTheSweepLeavesAMatchThatSettledItself(t *testing.T) {
	ctx, p := testPool(t)
	users, matches, a, b, _ := settled(t, ctx, p, nil)

	before := users.rating(ctx, a.ID)
	if _, err := p.Exec(ctx,
		`UPDATE match_submissions SET received_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}

	v := verifier{matches: matches, dataVersion: loadRoster()}
	if v.sweepUnilateral(ctx) {
		t.Error("the sweep picked up a match that both players reported on")
	}
	if got := users.rating(ctx, a.ID); got != before {
		t.Errorf("%+v became %+v", before, got)
	}
	_ = b
}

// **A plausibility anomaly flags an account and changes nothing about the
// match.** An assist bot plays a real match: every hash it reports is correct,
// so checks 1 and 2 have nothing to say, and voiding the result would punish
// the opponent for somebody else's client.
func TestAnImplausibleMatchIsFlaggedButStillCounts(t *testing.T) {
	ctx, p := testPool(t)
	users, matches, a, b := twoPlayers(t, ctx, p)

	// A log whose seat 1 is a twenty-frame loop, played straight: it verifies,
	// because it is a real simulation, and it looks nothing like a hand.
	logBytes, checksums, frames := macroMatch(t)
	s := submission{
		winner: 1, endFrame: frames, chars: [2]int{0, 1},
		disconnect: true, inputLog: logBytes, checksums: checksums,
	}

	id, err := matches.create(ctx, "ranked", a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := matches.record(ctx, id, a.ID, s); err != nil {
		t.Fatal(err)
	}
	if _, err := matches.record(ctx, id, b.ID, s); err != nil {
		t.Fatal(err)
	}
	row, _ := matches.byID(ctx, id)
	if err := matches.settle(ctx, row, s); err != nil {
		t.Fatal(err)
	}

	v := verifier{matches: matches, dataVersion: loadRoster()}
	if !v.step(ctx) {
		t.Fatal("the worker found no job")
	}

	// The match stands.
	if _, verified := jobResult(t, ctx, p, id); verified != "ok" {
		t.Errorf("the match was voided over a plausibility flag: %q", verified)
	}
	if ra := users.rating(ctx, a.ID); ra.LP != lpBase {
		t.Errorf("the ladder was rolled back: %+v", ra)
	}

	// The account is flagged, and only the one the anomaly named.
	var p1Flagged, p2Flagged bool
	p.QueryRow(ctx, `SELECT flagged FROM users WHERE id = $1`, a.ID).Scan(&p1Flagged)
	p.QueryRow(ctx, `SELECT flagged FROM users WHERE id = $1`, b.ID).Scan(&p2Flagged)
	if !p1Flagged {
		t.Error("the seat that played like a macro was not flagged")
	}
	if p2Flagged {
		t.Error("the other player was flagged for their opponent's inputs")
	}

	// And the numbers are kept, because the signal is across matches rather
	// than inside one of them.
	var result map[string]any
	if err := p.QueryRow(ctx, `SELECT result FROM verification_jobs WHERE match_id = $1`, id).
		Scan(&result); err != nil {
		t.Fatal(err)
	}
	if list, ok := result["anomalies"].([]any); !ok || len(list) == 0 {
		t.Errorf("the job recorded no anomalies: %v", result)
	}
}

// macroMatch plays a match where one seat loops a fixed input, which verifies
// as a real simulation and reads as a script.
func macroMatch(t *testing.T) (log, sums []byte, frames int) {
	t.Helper()
	version := loadRoster()

	setup := sim.Setup{Chars: [2]int32{0, 1}}
	s := sim.NewSessionOf(setup)
	loop := []uint16{
		sim.InRight, sim.InRight, sim.InDown, sim.InDown | sim.InRight,
		sim.InRight | sim.InLP, 0, 0, 0,
	}

	var inputs [][2]uint16
	var packed []byte
	for f := range 1800 {
		in := [2]uint16{loop[f%len(loop)], 0}
		s.Advance(in)
		inputs = append(inputs, in)
		if s.Frame()%checksumEvery == 0 {
			packed = binary.LittleEndian.AppendUint32(packed, s.Checksum())
		}
	}
	return replaylog.Encode(setup, version, inputs), packed, len(inputs)
}
