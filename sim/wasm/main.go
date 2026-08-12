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

	api.Set("reset", js.FuncOf(func(js.Value, []js.Value) any {
		session = sim.NewSession()
		session.State().WriteSnapshot(snap[:])
		return nil
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
