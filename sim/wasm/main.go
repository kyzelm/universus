//go:build js && wasm

// Command wasm is the sim's WASM entrypoint. It owns the game state and a
// single snapshot buffer in linear memory; JS drives it one frame at a time
// and reads the buffer through a DataView. No state crosses as a JS value.
package main

import (
	"syscall/js"
	"unsafe"

	"universus/data"
	"universus/sim"
)

var (
	session = sim.NewSession()
	snap    [sim.SnapshotSize]byte

	// The scripted opponent played as a device, per seat, for the bot-vs-bot
	// harness. Outside the session on purpose: a device is an input source, not
	// part of the match, and nothing it remembers is checksummed.
	devices [2]*sim.AIDevice
)

func main() {
	// The roster loads before the first state exists. Character data is part of
	// the simulation's identity: this binary and the native one must agree on
	// it or every checksum differs.
	cs, err := data.Load()
	if err != nil {
		panic("character data failed to load: " + err.Error())
	}
	if !sim.LoadCharacters(cs) {
		panic("sim refused the embedded roster")
	}
	// The balance data is loaded for the same reason and with the same
	// consequence: an unloaded balance is a match with no Drive costs.
	b, err := data.LoadBalance()
	if err != nil {
		panic("balance data failed to load: " + err.Error())
	}
	sim.LoadBalance(b)
	session = sim.NewSession()

	done := make(chan struct{})

	api := js.Global().Get("Object").New()

	// advance(p1, p2) runs one frame from two input bitfields and refreshes
	// the snapshot. One call per frame, and per rollback replay frame.
	api.Set("advance", js.FuncOf(func(_ js.Value, args []js.Value) any {
		session.Advance([2]uint16{uint16(args[0].Int()), uint16(args[1].Int())})
		session.State().WriteSnapshot(snap[:])
		return nil
	}))

	// reset(training, ai, c0, c1) starts a fresh match from a setup. Every
	// argument picks part of the *state* rather than a switch the view holds:
	// the lab is a different match, not a different way of drawing one, and a
	// seat the AI is driving is a different match for the same reason — the
	// opponent's inputs are generated inside the sim, so a client that
	// disagreed about the tier would disagree about every frame.
	//
	// These are exactly the fields a replay log's header carries, because they
	// are exactly the ones an input log cannot recover on its own.
	api.Set("reset", js.FuncOf(func(_ js.Value, args []js.Value) any {
		arg := func(i int) int {
			if len(args) > i {
				return args[i].Int()
			}
			return 0
		}
		session = sim.NewSessionOf(sim.Setup{
			Training: len(args) > 0 && args[0].Truthy(),
			AI:       int32(arg(1)),
			Chars:    [2]int32{int32(arg(2)), int32(arg(3))},
		})
		session.State().WriteSnapshot(snap[:])
		return nil
	}))

	// aiPress(seat, tier, seed) is the scripted opponent played as a *device*:
	// it reads the state and returns a bitfield, and the caller feeds that in
	// wherever a keyboard's would go. Nothing is written to the match, so the
	// two ends of a harness run may press different things without that being
	// a desync (03 Game Design/AI Opponent.md).
	//
	// The device is created on the first call for a seat and lives until the
	// next reset, so its generator runs one uninterrupted sequence per match —
	// which is what makes a harness run reproducible.
	api.Set("aiPress", js.FuncOf(func(_ js.Value, args []js.Value) any {
		seat := args[0].Int()
		if seat < 0 || seat > 1 {
			return 0
		}
		if devices[seat] == nil {
			devices[seat] = sim.NewAIDevice(int32(args[1].Int()), uint32(args[2].Int()))
		}
		return int(devices[seat].Press(session.State(), seat))
	}))

	// rewind(frame) restores the state at the start of frame; the caller then
	// replays with advance(). The net layer drives the replay because it holds
	// the input history and knows which predictions the arriving packet just
	// invalidated. Reports false outside the rollback window.
	api.Set("rewind", js.FuncOf(func(_ js.Value, args []js.Value) any {
		ok := session.Rewind(uint32(args[0].Int()))
		session.State().WriteSnapshot(snap[:])
		return ok
	}))

	// eventsAt(frame) is what happened on a frame — hit, block, throw,
	// knockdown, super — packed with player 0 in the low 16 bits. The view
	// calls it only for frames the net layer has confirmed, which is what
	// keeps one hit from being drawn once per rollback replay.
	api.Set("eventsAt", js.FuncOf(func(_ js.Value, args []js.Value) any {
		return int(session.EventsAt(uint32(args[0].Int())))
	}))

	// numCharacters() is how many entries the roster actually has. The view
	// needs it to keep a character index out of the URL from naming somebody
	// who does not exist: CharacterAt answers with the zero character rather
	// than crashing inside a rollback replay, which is right for the sim and
	// reads on screen as a fighter who will not move.
	api.Set("numCharacters", js.FuncOf(func(js.Value, []js.Value) any {
		return int(sim.NumCharacters())
	}))

	// stageHalfWidth() and cameraHalfWidth() are the walls and the screen, in
	// whole units, so the view frames what the sim collides against rather
	// than a copy of it.
	api.Set("stageHalfWidth", js.FuncOf(func(js.Value, []js.Value) any {
		return sim.BalanceOf().StageHalfWidth.ToInt()
	}))
	api.Set("cameraHalfWidth", js.FuncOf(func(js.Value, []js.Value) any {
		return sim.BalanceOf().CameraHalfWidth.ToInt()
	}))

	// characterName(i) is the roster entry's display name, for the character
	// select. A string crossing the boundary is fine here for the reason
	// GameState never does: this is immutable reference data read once when a
	// menu is drawn, not per-frame state.
	api.Set("characterName", js.FuncOf(func(_ js.Value, args []js.Value) any {
		i := int32(args[0].Int())
		if i < 0 || i >= sim.NumCharacters() {
			return ""
		}
		return sim.CharacterAt(i).Name
	}))

	// dataVersion() is the hash of the embedded character files. Both ends
	// compare it before the first frame: different frame data is a desync that
	// no amount of checksum exchange can diagnose after the fact.
	api.Set("dataVersion", js.FuncOf(func(js.Value, []js.Value) any {
		v, err := data.Version()
		if err != nil {
			return 0
		}
		return v
	}))

	// dump() is what it compares when the checksums already disagree: the
	// whole state, one field per line, so the gate can report *which* field
	// diverged. A hash cannot answer that by construction.
	api.Set("dump", js.FuncOf(func(js.Value, []js.Value) any {
		return session.State().Dump()
	}))

	// frame() is the frame the sim is on. The net layer keeps its own count,
	// so this is for a caller that has to compare two sims against each other —
	// the bot-vs-bot harness lines both ends up on the same frame before it
	// hashes them.
	api.Set("frame", js.FuncOf(func(js.Value, []js.Value) any {
		return int(session.Frame())
	}))

	// checksum() is what the native-vs-WASM differential compares.
	api.Set("checksum", js.FuncOf(func(js.Value, []js.Value) any {
		return session.Checksum()
	}))

	api.Set("snapshotPtr", js.FuncOf(func(js.Value, []js.Value) any {
		return int(uintptr(unsafe.Pointer(&snap[0])))
	}))
	api.Set("snapshotLen", js.FuncOf(func(js.Value, []js.Value) any {
		return len(snap)
	}))
	// noop() exists only for the benchmark: it isolates the cost of crossing the
	// JS/Go boundary from the cost of the sim, which turn out to differ by
	// almost three orders of magnitude. Subtract it from any other number here.
	api.Set("noop", js.FuncOf(func(js.Value, []js.Value) any {
		return nil
	}))
	api.Set("quit", js.FuncOf(func(js.Value, []js.Value) any {
		close(done)
		return nil
	}))

	js.Global().Set("sim", api)

	// Frame 0 is drawable before the first advance.
	session.State().WriteSnapshot(snap[:])

	<-done
}
