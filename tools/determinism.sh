#!/bin/sh
# The determinism gate: run an input log through the native sim and the WASM
# sim, compare per-frame checksums, fail on the first divergence.
#
# This is the most important test in the project. If it passes, the same binary
# logic runs on both sides of the wire; if it fails, nothing downstream matters.
#
# Usage: tools/determinism.sh [log ...]   (default: everything in testdata/)
set -eu

cd "$(dirname "$0")/.."
logs=${*:-$(ls testdata/*.inputs)}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

GOOS=js GOARCH=wasm go build -o "$tmp/main.wasm" ./sim/wasm

for log in $logs; do
	go run ./tools/replay run "$log" >"$tmp/native.txt"
	node tools/wasm-replay.mjs "$tmp/main.wasm" "$log" >"$tmp/wasm.txt"

	if diff "$tmp/native.txt" "$tmp/wasm.txt" >"$tmp/diff.txt"; then
		echo "determinism gate OK: $log, $(wc -l <"$tmp/native.txt" | tr -d ' ') checksums identical"
	else
		echo "determinism gate FAILED: $log — native vs WASM diverged"
		echo "first divergence (< native, > wasm):"
		head -4 "$tmp/diff.txt"
		exit 1
	fi
done
