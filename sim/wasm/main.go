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
	api.Set("quit", js.FuncOf(func(js.Value, []js.Value) any {
		close(done)
		return nil
	}))

	js.Global().Set("sim", api)

	// Frame 0 is drawable before the first advance.
	session.State().WriteSnapshot(snap[:])

	<-done
}
