package main

import (
	"encoding/binary"
	"strings"
	"sync"
	"testing"

	"universus/data"
	"universus/sim"
	"universus/sim/replaylog"
)

// The shipped roster, loaded once. The verification worker re-simulates with
// the same character data the clients played on, so a test of it has to be
// running the same sim the server does rather than a fixture.
var loadRoster = sync.OnceValue(func() uint32 {
	cs, err := data.Load()
	if err != nil || !sim.LoadCharacters(cs) {
		panic("the sim refused the embedded roster")
	}
	b, err := data.LoadBalance()
	if err != nil {
		panic(err)
	}
	sim.LoadBalance(b)

	v, err := data.Version()
	if err != nil {
		panic(err)
	}
	return v
})

// playedMatch runs a match to its end and returns exactly what an honest client
// would upload: the log, the checkpoint hashes, the winner and the last frame.
func playedMatch(t *testing.T) (logBytes, checksums []byte, winner, endFrame int) {
	t.Helper()
	version := loadRoster()

	setup := sim.Setup{Chars: [2]int32{0, 1}}
	s := sim.NewSessionOf(setup)

	var inputs [][2]uint16
	var sums []uint32
	// Seat 1 walks in and swings; seat 2 stands there. The match ends by KO
	// rather than by timeout, which keeps the log short enough to read.
	for s.State().Winner == sim.RoundNobody {
		in := [2]uint16{sim.InRight, 0}
		if len(inputs)%12 < 2 {
			in[0] = sim.InRight | sim.InHP
		}
		s.Advance(in)
		inputs = append(inputs, in)
		if s.Frame()%checksumEvery == 0 {
			sums = append(sums, s.Checksum())
		}
		if len(inputs) > 200_000 {
			t.Fatal("the match never ended")
		}
	}

	packed := make([]byte, len(sums)*4)
	for i, sum := range sums {
		binary.LittleEndian.PutUint32(packed[i*4:], sum)
	}
	return replaylog.Encode(setup, version, inputs), packed,
		int(s.State().Winner) + 1, int(s.Frame())
}

func TestAnHonestMatchVerifies(t *testing.T) {
	logBytes, checksums, winner, endFrame := playedMatch(t)
	v := verifier{dataVersion: loadRoster()}

	got := v.resimulate(logBytes, checksums, winner, endFrame, false)
	if !got.ok {
		t.Fatalf("an honest match was rejected: %s (frame %d)", got.reason, got.frame)
	}
}

// **The comparison is the checkpoint hashes, not the final state.** A match
// that diverges in the middle and lands somewhere plausible is still a match
// that did not happen, and the frame they first disagree on is the only
// diagnostic worth having.
func TestATamperedChecksumIsCaughtAtTheFrameItHappened(t *testing.T) {
	logBytes, checksums, winner, endFrame := playedMatch(t)
	v := verifier{dataVersion: loadRoster()}

	// Break the third checkpoint, which is frame 90.
	checksums[2*4] ^= 0xff
	got := v.resimulate(logBytes, checksums, winner, endFrame, false)

	if got.ok {
		t.Fatal("a tampered checksum verified")
	}
	if got.frame != 3*checksumEvery {
		t.Errorf("reported frame %d, want %d", got.frame, 3*checksumEvery)
	}
}

func TestAMisreportedOutcomeIsCaught(t *testing.T) {
	logBytes, checksums, winner, endFrame := playedMatch(t)
	v := verifier{dataVersion: loadRoster()}

	for _, c := range []struct {
		name             string
		winner, endFrame int
		wantIn           string
	}{
		{"the loser claims the win", 3 - winner, endFrame, "won, reported"},
		{"a frame count that is not the one it ended on", winner, endFrame + 1, "ended on frame"},
	} {
		got := v.resimulate(logBytes, checksums, c.winner, c.endFrame, false)
		if got.ok {
			t.Errorf("%s: verified", c.name)
		} else if !strings.Contains(got.reason, c.wantIn) {
			t.Errorf("%s: %q does not mention %q", c.name, got.reason, c.wantIn)
		}
	}
}

// A log recorded against different frame data replays into a different match
// through no fault of either player. **Not a verdict about them**, which is why
// it is a reason rather than a mismatch anybody gets flagged for.
func TestDataFromAnotherBuildIsNotAnAccusation(t *testing.T) {
	logBytes, checksums, winner, endFrame := playedMatch(t)

	v := verifier{dataVersion: loadRoster() ^ 1}
	got := v.resimulate(logBytes, checksums, winner, endFrame, false)
	if got.ok {
		t.Fatal("a log from another build verified")
	}
	if !strings.Contains(got.reason, "data version") {
		t.Errorf("reason %q does not name the data version", got.reason)
	}
}

func TestAnUnreadableLogIsRefusedRatherThanReplayed(t *testing.T) {
	v := verifier{dataVersion: loadRoster()}

	for _, c := range []struct {
		name string
		log  []byte
	}{
		{"empty", nil},
		{"not a log", []byte("hello there, this is not an input log")},
		{"header only", replaylog.Encode(sim.Setup{}, loadRoster(), nil)},
	} {
		if got := v.resimulate(c.log, nil, 1, 100, false); got.ok {
			t.Errorf("%s: verified", c.name)
		}
	}
}

// The count has to match on both sides: a client that uploads fewer hashes than
// its log contains checkpoints is a client that stopped reporting partway.
func TestTheCheckpointCountsHaveToAgree(t *testing.T) {
	logBytes, checksums, winner, endFrame := playedMatch(t)
	v := verifier{dataVersion: loadRoster()}

	short := v.resimulate(logBytes, checksums[:len(checksums)-4], winner, endFrame, false)
	if short.ok || !strings.Contains(short.reason, "checkpoints") {
		t.Errorf("a short series gave %+v", short)
	}

	long := v.resimulate(logBytes, append(checksums, 0, 0, 0, 0), winner, endFrame, false)
	if long.ok || !strings.Contains(long.reason, "checkpoints") {
		t.Errorf("a long series gave %+v", long)
	}
}

// A disconnect match's log stops mid-match by definition: the winner was
// decided by policy rather than by the simulation. Asking the replay who won
// would fail every honest disconnect, so it is not asked — and what is still
// checked is that the log replays to its own hashes.
func TestADisconnectIsNotAskedWhoWon(t *testing.T) {
	logBytes, checksums, _, endFrame := playedMatch(t)
	v := verifier{dataVersion: loadRoster()}

	// Cut the log short, which is what a match that ended early looks like.
	parsed, err := replaylog.Decode("test", logBytes)
	if err != nil {
		t.Fatal(err)
	}
	half := len(parsed.Inputs) / 2
	half -= half % checksumEvery // end on a checkpoint, as the client does
	short := replaylog.Encode(parsed.Setup, loadRoster(), parsed.Inputs[:half])
	shortSums := checksums[:(half/checksumEvery)*4]

	if got := v.resimulate(short, shortSums, 1, half, true); !got.ok {
		t.Errorf("an honest disconnect was rejected: %s (frame %d)", got.reason, got.frame)
	}
	// The same log judged as a completed match is refused, which is what says
	// the flag is doing the work rather than the check being absent.
	if got := v.resimulate(short, shortSums, 1, half, false); got.ok {
		t.Error("a half a match verified as a finished one")
	}

	// And a tampered disconnect log is still caught: the hashes are the check
	// that survives having only one report.
	bad := append([]byte(nil), shortSums...)
	bad[4] ^= 0xff
	if got := v.resimulate(short, bad, 1, half, true); got.ok {
		t.Error("a tampered disconnect log verified")
	}
	_ = endFrame
}
