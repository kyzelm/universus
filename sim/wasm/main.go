//go:build js && wasm

// Command wasm is the sim's WASM entrypoint. It owns the game state and a
// single snapshot buffer in linear memory; JS drives it one frame at a time
// and reads the buffer through a DataView. No state crosses as a JS value.
package main

import (
	"syscall/js"
	"unsafe"

	"universus/sim"
)

var (
	session = sim.NewSession()
	snap    [sim.SnapshotSize]byte
)

func main() {
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

	// adjust(frame, p1, p2) corrects a mispredicted input: one call rewinds and
	// replays every frame since, so the boundary is crossed once per rollback,
	// not once per replayed frame. Reports false outside the rollback window.
	api.Set("adjust", js.FuncOf(func(_ js.Value, args []js.Value) any {
		in := [2]uint16{uint16(args[1].Int()), uint16(args[2].Int())}
		ok := session.Adjust(uint32(args[0].Int()), in)
		session.State().WriteSnapshot(snap[:])
		return ok
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
