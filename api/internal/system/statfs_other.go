//go:build !unix

package system

import "errors"

// diskFreeBytes is unsupported off unix; the caller treats the error as
// "stat unwired" and surfaces a zero DiskFreeBytes. Maktaba's servers
// ship on linux, so this branch only exists to keep `go build` green on
// other GOOS during local tooling.
func diskFreeBytes(string) (uint64, error) {
	return 0, errors.New("disk-free probe unsupported on this platform")
}
