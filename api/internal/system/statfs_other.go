//go:build !unix && !windows

package system

import "errors"

// diskFreeBytes has no probe on this platform (neither unix nor windows,
// e.g. js/wasm or plan9); the caller treats the error as "stat unwired"
// and surfaces a zero DiskFreeBytes, and the update disk check is
// skipped.
func diskFreeBytes(string) (uint64, error) {
	return 0, errors.New("disk-free probe unsupported on this platform")
}
