package sim

import (
	"fmt"
	"reflect"
	"strings"
)

// Diagnostic only. Nothing here is called by Advance, and nothing here may ever
// influence state — it exists to answer "the checksums differ at frame N, which
// *field* differs?", which a hash cannot answer by construction.
//
// The risk register is explicit that this gets built before it is needed: a
// desync that resists diagnosis is diagnosed by binary-searching the checksum
// log to a frame and then diffing full state at it, and writing that tooling
// while panicking is expensive. M2 puts real netcode in front of real players,
// which is when the first one arrives.
//
// reflect is used deliberately and only here. It walks fields in declaration
// order, so two builds produce identical text, and adding a field to GameState
// makes it appear in the dump with no edit — which is the property that matters,
// since the field nobody remembered to print is the one causing the desync.

// Dump renders the whole state as one field per line, in declaration order.
//
// Designed to be diffed: stable line order, one value per line, and array
// elements addressed individually so a diff points at the index rather than
// re-printing the array.
func (s *GameState) Dump() string {
	var b strings.Builder
	dumpValue(&b, "", reflect.ValueOf(*s))
	return b.String()
}

func dumpValue(b *strings.Builder, path string, v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			name := t.Field(i).Name
			if path != "" {
				name = path + "." + name
			}
			dumpValue(b, name, v.Field(i))
		}

	case reflect.Array:
		// Zero runs are collapsed: the input history is 32 entries per player
		// and is mostly zero, and 60 lines of "0" per dump buries the two that
		// matter. The count is printed so a differing run length still shows.
		n := v.Len()
		for i := 0; i < n; {
			if !isZero(v.Index(i)) {
				dumpValue(b, fmt.Sprintf("%s[%d]", path, i), v.Index(i))
				i++
				continue
			}
			j := i
			for j < n && isZero(v.Index(j)) {
				j++
			}
			if j-i == 1 {
				dumpValue(b, fmt.Sprintf("%s[%d]", path, i), v.Index(i))
			} else {
				fmt.Fprintf(b, "%s[%d:%d] = zero\n", path, i, j)
			}
			i = j
		}

	default:
		fmt.Fprintf(b, "%s = %v\n", path, v.Interface())
	}
}

func isZero(v reflect.Value) bool {
	return v.Kind() != reflect.Struct && v.Kind() != reflect.Array && v.IsZero()
}
