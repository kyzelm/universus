//go:build js && wasm

package main

import (
	"syscall/js"
	"unsafe"
)

var snapshotBuf [64]byte

func main() {
	done := make(chan struct{})

	sim := js.Global().Get("Object").New()
	sim.Set("add", js.FuncOf(func(_ js.Value, args []js.Value) any {
		return args[0].Int() + args[1].Int()
	}))
	sim.Set("snapshotPtr", js.FuncOf(func(js.Value, []js.Value) any {
		return int(uintptr(unsafe.Pointer(&snapshotBuf[0])))
	}))
	sim.Set("snapshotLen", js.FuncOf(func(js.Value, []js.Value) any {
		return len(snapshotBuf)
	}))
	sim.Set("quit", js.FuncOf(func(js.Value, []js.Value) any {
		close(done)
		return nil
	}))
	js.Global().Set("sim", sim)

	<-done
}
