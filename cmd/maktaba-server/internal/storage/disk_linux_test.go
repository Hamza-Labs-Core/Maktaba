//go:build linux

package storage

import "testing"

// A read-only mount (a library mounted :ro in a container) is never offered for the data
// folder: no database can be made there (Epic 59, found by following the NAS guides).
func TestMountReadOnly(t *testing.T) {
	for opts, want := range map[string]bool{
		"ro,relatime":                       true,
		"rw,relatime":                       false,
		"rw,nosuid,nodev,errors=remount-ro": false,
		"ro":                                true,
		"":                                  false,
	} {
		if got := mountReadOnly(opts); got != want {
			t.Errorf("mountReadOnly(%q) = %v, want %v", opts, got, want)
		}
	}
}
