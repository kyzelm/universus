package sim

import "testing"

// events runs the fixture's jab into player 1 and returns the state on the
// frame it connected, with p1 holding hold — which is what decides whether it
// is a hit or a block.
func events(t *testing.T, hold uint16) GameState {
	t.Helper()

	s := facing(40)
	for range 40 {
		s.Advance([2]uint16{InLP, hold})
		switch s.Players[1].State {
		case StateHitstun, StateBlockstun:
			return s
		}
	}
	t.Fatal("the jab never connected")
	return GameState{}
}

// The flag is on the defender, because that is where the effect happens: a hit
// spark is drawn on the fighter who was hit, not on the one who swung.
func TestAHitFlagsTheDefender(t *testing.T) {
	s := events(t, 0)

	if s.Players[1].Events&EventHit == 0 {
		t.Error("the defender has no hit event")
	}
	if s.Players[1].Events&EventBlock != 0 {
		t.Error("a clean hit also flagged a block")
	}
	if s.Players[0].Events != 0 {
		t.Errorf("the attacker has events %#x, want none", s.Players[0].Events)
	}
}

func TestABlockFlagsABlock(t *testing.T) {
	s := events(t, InRight) // holding away from the attacker

	if s.Players[1].Events&EventBlock == 0 {
		t.Error("the defender has no block event")
	}
	if s.Players[1].Events&EventHit != 0 {
		t.Error("a blocked hit also flagged a clean one")
	}
}

// **A flag describes one frame and does not survive it.** This is the bug in
// its slower form: a flag that outlived its frame would be fired again on every
// confirmed frame it survived into, and the hit's own hitstop is six frames of
// exactly that.
func TestAnEventLastsOneFrame(t *testing.T) {
	s := events(t, 0)
	if s.Hitstop == 0 {
		t.Fatal("the fixture's jab has no hitstop, so this proves nothing")
	}

	for f := range 10 {
		s.Advance([2]uint16{0, 0})
		if e := s.Players[1].Events; e&EventHit != 0 {
			t.Fatalf("the hit event is still set %d frames later (hitstop %d)", f+1, s.Hitstop)
		}
	}
}

// session is a Session with the players close enough to hit each other, which
// New() deliberately is not: a match starts at a range where nothing connects.
func session() Session {
	s := NewSession()
	s.State().Players[0].X = FromInt(-20)
	s.State().Players[1].X = FromInt(20)
	return s
}

// hits runs the jab and returns the frame it connected on.
func hits(t *testing.T, s *Session, hold uint16) uint32 {
	t.Helper()

	for range 40 {
		s.Advance([2]uint16{InLP, hold})
		switch s.State().Players[1].State {
		case StateHitstun, StateBlockstun:
			return s.Frame() - 1
		}
	}
	t.Fatal("the jab never connected")
	return 0
}

// defender pulls player 1's flags out of a packed event word.
func defender(bits uint32) int32 { return int32(bits >> 16) }

// **The mechanism, in one test.** A rolled-back frame is simulated again with
// better inputs, and the events it produces must *replace* what it produced on
// the guess rather than add to it. A view reading the union would draw the hit
// that never happened alongside the block that did — which is the classic
// rollback bug in its visible form.
func TestACorrectedReplayReplacesTheEvents(t *testing.T) {
	s := session()
	hit := hits(t, &s, 0)

	if e := defender(s.EventsAt(hit)); e&EventHit == 0 {
		t.Fatalf("frame %d: events %#x, want a hit to correct away", hit, e)
	}

	// The packet arrives: the defender was holding back all along, so that
	// frame was a block and not a hit.
	if !s.Adjust(hit, [2]uint16{InLP, InRight}) {
		t.Fatal("the rollback was rejected")
	}

	e := defender(s.EventsAt(hit))
	if e&EventBlock == 0 {
		t.Errorf("frame %d: events %#x after the correction, want a block", hit, e)
	}
	if e&EventHit != 0 {
		t.Errorf("frame %d: the corrected frame still reports the hit that was predicted (%#x)", hit, e)
	}
}

// A frame replayed nine times reports what one simulation of it reports. The
// slot is keyed by the frame, so a replay overwrites rather than appends.
func TestReplayingAFrameDoesNotRepeatItsEvents(t *testing.T) {
	const frames = 40
	in := inputSeq(frames)

	ref := session()
	for _, i := range in {
		ref.Advance(i)
	}

	s := session()
	for f, i := range in {
		s.Advance(i)
		// Roll back and replay the same eight frames, over and over: nine
		// simulations of every frame in the window.
		if f >= MaxRollback && f%3 == 0 {
			target := s.Frame() - MaxRollback
			if !s.Adjust(target, in[target]) {
				t.Fatalf("frame %d: rollback rejected", f)
			}
		}
	}

	var any uint32
	for f := uint32(0); f < frames; f++ {
		if got, want := s.EventsAt(f), ref.EventsAt(f); got != want {
			t.Errorf("frame %d: events %#x after replays, want %#x", f, got, want)
		}
		any |= ref.EventsAt(f)
	}
	if any == 0 {
		t.Fatal("no events at all in 40 frames, so this test is checking nothing")
	}
}

// A frame that has fallen out of the ring reports nothing rather than another
// frame's events. The ring wraps, and a slot keyed only by position would
// answer for the frame a lap later — a hit spark drawn on a frame where nobody
// was hit, seconds after the hit that produced it.
func TestEventsFallOutOfTheRingCleanly(t *testing.T) {
	s := session()

	// Jabs on a loop, so events are scattered across the ring rather than
	// confined to one frame nothing aliases to.
	for f := range eventRing * 3 {
		var in uint16
		if f%12 < 2 {
			in = InLP
		}
		s.Advance([2]uint16{in, 0})
	}

	// Everything older than one lap is gone, and the slots those frames index
	// are held by frames that do have events — which is what makes a stale read
	// possible and this test worth running.
	var aliased uint32
	for f := uint32(0); f+eventRing < s.Frame(); f++ {
		if got := s.EventsAt(f); got != 0 {
			t.Errorf("frame %d is over a lap old but reports %#x", f, got)
		}
		aliased |= s.EventsAt(f + eventRing)
	}
	if aliased == 0 {
		t.Fatal("no events in the slots the aged-out frames index, so this test is checking nothing")
	}
}

// The packing the WASM boundary reads: player 0 low, player 1 high.
func TestEventsPackPerPlayer(t *testing.T) {
	var s GameState
	s.Players[0].Events = EventHit
	s.Players[1].Events = EventSuper

	got := s.packEvents()
	if want := uint32(EventHit) | uint32(EventSuper)<<16; got != want {
		t.Errorf("packEvents = %#x, want %#x", got, want)
	}
}
