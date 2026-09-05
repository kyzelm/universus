package sim

import "testing"

// betweenRounds advances until the fight resumes or the match is over, and
// reports how many frames that took. Every transition is simulation state with
// a fixed length, so this terminates or the state machine is broken — the cap
// is there to fail the test rather than hang it.
func betweenRounds(t *testing.T, s *GameState) int {
	t.Helper()

	for f := 1; f <= 1000; f++ {
		s.Advance([2]uint16{})
		if s.Phase == PhaseFight || s.Phase == PhaseMatchEnd {
			return f
		}
	}
	t.Fatalf("the match never came out of phase %d", s.Phase)
	return 0
}

// koRound empties a player's health and runs the frame that notices. Health is
// set directly rather than beaten off: what is under test is the round rule,
// and the hit that produced the zero has its own tests.
func koRound(s *GameState, loser int) {
	s.Players[loser].Health = 0
	s.Advance([2]uint16{})
}

func TestAKOEndsTheRoundAndAwardsIt(t *testing.T) {
	s := New()
	koRound(&s, 1)

	if s.Phase != PhaseKO {
		t.Fatalf("phase %d after a KO, want the freeze", s.Phase)
	}
	if s.Wins != [2]int32{1, 0} {
		t.Errorf("wins %v after player 0 took the round", s.Wins)
	}
	if s.RoundWinner != 0 {
		t.Errorf("round winner %d, want player 0", s.RoundWinner)
	}

	betweenRounds(t, &s)
	if s.Phase != PhaseFight || s.Round != 2 {
		t.Errorf("phase %d round %d, want the second round fighting", s.Phase, s.Round)
	}
}

// The freeze, the result and the intro are simulation state with fixed lengths,
// because what they decide is when input resumes. A view that timed them would
// resume input on a different frame on each machine.
func TestTheRoundTransitionTakesTheFramesTheBalanceSays(t *testing.T) {
	s := New()
	koRound(&s, 1)

	b := BalanceOf()
	// One frame of the freeze has already run: the KO frame itself entered the
	// phase, and the frame after it is the first the phase clock counts.
	want := int(b.KOFreeze + b.RoundEndHold + b.IntroFrames)
	if got := betweenRounds(t, &s); got != want {
		t.Errorf("the transition took %d frames, want %d", got, want)
	}
}

// Nothing moves between rounds. Input is read into the history — it has to be,
// or the ring has a hole in it — but nothing acts on it.
func TestNothingHappensBetweenRounds(t *testing.T) {
	s := New()
	koRound(&s, 1)

	before := s.Players[0]
	timer := s.Timer
	for range 5 {
		s.Advance([2]uint16{InRight | InLP, InLeft | InLP})
	}

	if p := s.Players[0]; p.State != before.State || p.X != before.X {
		t.Errorf("player 0 acted during the KO freeze: %d at %d, was %d at %d",
			p.State, p.X, before.State, before.X)
	}
	if s.Timer != timer {
		t.Errorf("the round clock ran during the KO freeze: %d, was %d", s.Timer, timer)
	}
}

// A timeout goes to the higher *percentage* of a player's own pool. The two
// fixture characters have different pools, which is the only way this test can
// tell the rule from the plausible one: player 1 has less health and more of it.
func TestATimeoutIsDecidedOnPercentage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		health0    int32
		health1    int32
		wantWinner int32
		wantWins   [2]int32
	}{
		// 30% against 40%, and player 0 has the larger absolute number.
		{"fewer points, more percent", 300, 200, 1, [2]int32{0, 1}},
		{"more points and more percent", 800, 200, 0, [2]int32{1, 0}},
		// Exactly level as a percentage is a draw, and a draw awards nobody.
		{"equal percentages draw", 500, 250, RoundNobody, [2]int32{0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMatch(0, 1)
			s.Players[0].Health, s.Players[1].Health = tc.health0, tc.health1

			s.Timer = 1
			s.Advance([2]uint16{})

			if s.Phase != PhaseKO {
				t.Fatalf("phase %d, want the round to have ended on time", s.Phase)
			}
			if s.RoundWinner != tc.wantWinner {
				t.Errorf("round winner %d, want %d", s.RoundWinner, tc.wantWinner)
			}
			if s.Wins != tc.wantWins {
				t.Errorf("wins %v, want %v", s.Wins, tc.wantWins)
			}
		})
	}
}

// Both players reaching zero on the same frame is a draw, not a win for
// whichever the loop happened to check first.
func TestADoubleKOIsADraw(t *testing.T) {
	s := New()
	s.Players[0].Health = 0
	koRound(&s, 1)

	if s.RoundWinner != RoundNobody {
		t.Errorf("round winner %d after a double KO, want nobody", s.RoundWinner)
	}
	if s.Wins != [2]int32{} {
		t.Errorf("wins %v after a double KO, want neither awarded", s.Wins)
	}

	betweenRounds(t, &s)
	if s.Phase != PhaseFight {
		t.Errorf("phase %d, want the match to continue after a draw", s.Phase)
	}
}

// The round resets what the next round starts fresh with, and nothing else.
// Super carrying over is the rule that makes it the strategic gauge; the wins
// and the match damage total have to survive for the match to mean anything.
func TestANewRoundResetsHealthButCarriesSuper(t *testing.T) {
	s := New()
	s.Players[0].Super = 2 * BarUnits
	s.Players[0].Drive = 0
	s.Players[0].Burnout = 1
	s.Players[0].X = FromInt(100)
	s.Players[1].Health = 1
	s.Dealt = [2]int32{700, 0}
	s.Projectiles[0] = Projectile{Active: 1, Owner: 0, Move: 2, Life: 60}

	start := New()
	koRound(&s, 1)
	betweenRounds(t, &s)

	p := s.Players[0]
	switch {
	case p.Super != 2*BarUnits:
		t.Errorf("super %d after the round reset, want it carried over", p.Super)
	case p.Health != char().Health:
		t.Errorf("health %d, want the full pool %d", p.Health, char().Health)
	case p.Drive != DriveMax || p.Burnout != 0:
		t.Errorf("drive %d burnout %d, want a full gauge and no burnout", p.Drive, p.Burnout)
	case p.X != start.Players[0].X:
		t.Errorf("player 0 starts the round at %d, want %d", p.X, start.Players[0].X)
	}
	if s.Wins != [2]int32{1, 0} || s.Dealt != [2]int32{700, 0} {
		t.Errorf("wins %v damage %v: the match record did not survive the round", s.Wins, s.Dealt)
	}
	if s.Projectiles[0].Active != 0 {
		t.Error("a projectile outlived the round that fired it")
	}
	if s.Timer != BalanceOf().RoundFrames {
		t.Errorf("the new round's clock is %d, want a full %d", s.Timer, BalanceOf().RoundFrames)
	}
}

func TestTwoRoundWinsTakeTheMatch(t *testing.T) {
	s := New()

	koRound(&s, 1)
	betweenRounds(t, &s)
	if s.Phase != PhaseFight {
		t.Fatalf("the match ended on one round win: phase %d", s.Phase)
	}

	koRound(&s, 1)
	betweenRounds(t, &s)

	if s.Phase != PhaseMatchEnd || s.Winner != 0 {
		t.Fatalf("phase %d winner %d, want the match won by player 0", s.Phase, s.Winner)
	}

	// Terminal: the frame still advances, so the network and the checksum keep
	// moving, and the input history keeps being recorded — the ring may not
	// grow a hole in it just because the match is over. Nothing else moves.
	before := s
	for range 10 {
		s.Advance([2]uint16{InLP, InLP})
	}

	if s.Frame != before.Frame+10 {
		t.Errorf("frame %d, want %d: the sim stopped keeping time", s.Frame, before.Frame+10)
	}
	if s.Phase != PhaseMatchEnd || s.PhaseFrame != before.PhaseFrame {
		t.Errorf("phase %d frame %d, want the match end standing still", s.Phase, s.PhaseFrame)
	}
	for i, p := range s.Players {
		q := before.Players[i]
		if p.State != q.State || p.X != q.X || p.Health != q.Health {
			t.Errorf("player %d acted after the match ended: state %d at %d",
				i, p.State, p.X)
		}
	}
}

// Draws are unbounded on their own: nobody is awarded a round, so draw and
// replay is a loop with no exit. The cap ends it by the one value that is
// already tracked and already deterministic — total damage dealt (D54).
func TestTheRoundCapResolvesByDamageDealt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		dealt [2]int32
		want  int32
	}{
		{"more damage dealt wins", [2]int32{100, 900}, 1},
		// Arbitrary, deterministic and documented, which is the requirement.
		{"exactly equal goes to player 0", [2]int32{500, 500}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New()

			for r := int32(1); r <= BalanceOf().MaxRounds; r++ {
				if s.Round != r {
					t.Fatalf("round %d, want %d", s.Round, r)
				}
				s.Players[0].Health = 0
				koRound(&s, 1) // a double KO: a draw, so nobody banks a win
				s.Dealt = tc.dealt
				betweenRounds(t, &s)
			}

			if s.Wins != [2]int32{} {
				t.Fatalf("wins %v: the draws were not draws", s.Wins)
			}
			if s.Phase != PhaseMatchEnd {
				t.Fatalf("phase %d after %d rounds, want the cap to have ended the match",
					s.Phase, BalanceOf().MaxRounds)
			}
			if s.Winner != tc.want {
				t.Errorf("winner %d, want %d", s.Winner, tc.want)
			}
		})
	}
}

// Damage dealt is what came off the bar, which is what the tiebreak above is
// counting. Chip that stopped at 1 health counts what it removed and not what
// the formula asked for.
func TestDamageDealtTracksTheHealthBar(t *testing.T) {
	s := facing(30)
	s.Players[1].Health = 10

	for range 20 {
		s.Advance([2]uint16{InLP, 0})
		if s.Players[1].Health == 0 {
			break
		}
	}
	if s.Players[1].Health != 0 {
		t.Fatalf("the jab never landed: health %d", s.Players[1].Health)
	}
	if s.Dealt != [2]int32{10, 0} {
		t.Errorf("damage dealt %v, want the 10 the bar actually had", s.Dealt)
	}
}
