package sim

// Round flow — 03 Game Design/Round Flow.md. Best of three, 99 seconds a round,
// first to two round wins takes the match.
//
// **All of it is simulation state, transitions included.** The KO freeze, the
// result on screen and the next round's intro are fixed frame counts in
// GameState rather than animations the view plays, because what they really
// decide is *when input resumes* — and two machines that resume input on
// different frames have desynced, whatever their fighters were doing.

// Phases. Values are part of the state's byte layout, so they are appended to,
// never reordered.
const (
	// PhaseFight is the only one that runs the game. Every other phase freezes
	// both fighters and runs nothing but its own clock.
	PhaseFight int32 = iota
	PhaseKO
	PhaseRoundEnd
	PhaseIntro
	// PhaseMatchEnd is terminal: the match is over and nothing further happens
	// until the caller starts another one.
	PhaseMatchEnd
)

// RoundNobody is "no winner": a draw round, and the match while it is still
// being played. Not zero, because zero is player 0.
const RoundNobody int32 = -1

// updateRound is the round half of step 8: the clock, the KO check, and the
// decision to stop fighting. Runs after damage, so a hit landed this frame is
// already on the health bar it might have emptied.
//
// The timer pauses during hitstop for free — a frozen frame returns from
// Advance before step 8 — and that is the rule the design note asks for rather
// than a happy accident worth removing.
func (s *GameState) updateRound() {
	if s.Phase != PhaseFight {
		return
	}
	if s.Timer > 0 {
		s.Timer--
	}

	dead0, dead1 := s.Players[0].Health <= 0, s.Players[1].Health <= 0
	switch {
	// A double KO on the same frame is a draw, and it has to be checked before
	// either single KO or the player the loop reached first would win it.
	case dead0 && dead1:
		s.endRound(RoundNobody)
	case dead0:
		s.endRound(1)
	case dead1:
		s.endRound(0)
	case s.Timer == 0:
		s.endRound(s.aheadOnHealth())
	}
}

// aheadOnHealth is who wins a timeout: the higher percentage of their own
// maximum, not the higher number. The roster's characters do not share a health
// pool, so comparing absolutes would quietly hand every timeout to whoever
// picked the bigger one.
//
// Cross-multiplied rather than divided into a percentage: integer division
// calls 51% and 50.4% the same number, and the difference between them is a
// draw, which is a whole extra round. Both products stay far inside int32 —
// health is validated well below its square root for exactly this line.
func (s *GameState) aheadOnHealth() int32 {
	a := s.Players[0].Health * CharacterAt(s.Players[1].Char).Health
	b := s.Players[1].Health * CharacterAt(s.Players[0].Char).Health
	switch {
	case a > b:
		return 0
	case b > a:
		return 1
	}
	return RoundNobody
}

// endRound awards the round and starts the KO freeze. winner is RoundNobody for
// a draw, which awards the round to **neither** player (D53) and lets the match
// continue — the round cap below is what stops that being unbounded.
func (s *GameState) endRound(winner int32) {
	s.RoundWinner = winner
	if winner != RoundNobody {
		s.Wins[winner]++
	}
	// The freeze replaces whatever hitstop the killing blow set rather than
	// queueing behind it: two freezes in a row read as one long stutter, and
	// the hit that ends a round is the one moment nobody wants to sit through
	// twice.
	s.Hitstop = 0
	s.enterPhase(PhaseKO)
}

func (s *GameState) enterPhase(phase int32) {
	s.Phase = phase
	s.PhaseFrame = 0
}

// phaseLength is how long a between-rounds phase lasts. Frame counts, from the
// balance data like every other tunable: they change when the game feels wrong,
// and a client that changed one alone would resume input on a different frame
// from its opponent.
func phaseLength(phase int32) int32 {
	switch phase {
	case PhaseKO:
		return balance.KOFreeze
	case PhaseRoundEnd:
		return balance.RoundEndHold
	case PhaseIntro:
		return balance.IntroFrames
	}
	return 0
}

// advancePhase runs one frame of a between-rounds phase. It is the whole frame:
// the caller returns straight after, so nothing moves, nothing is hit and no
// input is read until the fight resumes.
func (s *GameState) advancePhase() {
	if s.Phase == PhaseMatchEnd {
		return // terminal — there is no next phase to count towards
	}

	s.PhaseFrame++
	if s.PhaseFrame < phaseLength(s.Phase) {
		return
	}

	switch s.Phase {
	case PhaseKO:
		s.enterPhase(PhaseRoundEnd)

	case PhaseRoundEnd:
		if w, over := s.matchWinner(); over {
			s.Winner = w
			s.enterPhase(PhaseMatchEnd)
			return
		}
		// The fighters are reset *before* the intro rather than after it, so
		// the intro is the players standing at their starting positions on full
		// health — which is what the intro is for.
		s.startRound()
		s.enterPhase(PhaseIntro)

	case PhaseIntro:
		s.enterPhase(PhaseFight)
	}
}

// matchWinner reports the match winner and whether the match is over at all.
//
// Two ways to end. Someone takes RoundsToWin rounds, which is the normal one;
// or the round cap runs out (D54) and the match resolves by **total damage
// dealt across the match** — a value already tracked, already deterministic,
// and needing no mechanism that does not exist.
//
// The cap exists because draws are unbounded otherwise: a draw awards nobody a
// round, so draw-and-replay is a loop with no exit, and "theoretically
// infinite" is not something to defend at a viva.
func (s *GameState) matchWinner() (int32, bool) {
	for i := int32(0); i < 2; i++ {
		if s.Wins[i] >= balance.RoundsToWin {
			return i, true
		}
	}
	if s.Round < balance.MaxRounds {
		return RoundNobody, false
	}

	switch {
	case s.Dealt[0] > s.Dealt[1]:
		return 0, true
	case s.Dealt[1] > s.Dealt[0]:
		return 1, true
	}
	// Arbitrary, and that is fine: an arbitrary rule both machines agree on is
	// correct, and a fair rule they could disagree about is a desync. It is
	// documented here and in the design note, and it needs two draws and
	// identical damage to the unit to ever be reached.
	return 0, true
}

// startRound resets the fighters for the next round.
//
// What it does *not* touch is the interesting half: **Super carries between
// rounds** (Resource System), and so do the round wins and the cumulative
// damage the cap's tiebreak reads. Everything else comes from a fresh match,
// so the reset cannot drift from the way a match starts — there is one
// definition of a fighter at the start of a round and this borrows it.
func (s *GameState) startRound() {
	fresh := NewMatch(s.Players[0].Char, s.Players[1].Char)
	for i := range s.Players {
		super := s.Players[i].Super
		s.Players[i] = fresh.Players[i]
		s.Players[i].Super = super
	}

	s.Round++
	s.Timer = balance.RoundFrames
	s.RoundWinner = RoundNobody
	s.Hitstop = 0
	// A fireball must not outlive the round that fired it: the pool is state
	// like everything else, and a projectile left in it would arrive during the
	// next round's intro.
	clear(s.Projectiles[:])
	s.updateCamera()
}
