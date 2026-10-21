package hostinfo

import (
	"path/filepath"
	"testing"
)

// The machine running the tests has memory and a disk with room on it.
func TestThisMachine(t *testing.T) {
	if TotalMemory() < 1<<30 {
		t.Fatalf("TotalMemory = %d: under 1 GB, or not read", TotalMemory())
	}
	dir := t.TempDir()
	free := FreeDisk(dir)
	if free == 0 {
		t.Fatal("FreeDisk of the temp dir is 0")
	}
	// a folder that does not exist yet is measured on the disk it would go on
	if got := FreeDisk(filepath.Join(dir, "not", "yet")); got == 0 {
		t.Fatal("FreeDisk of a folder to be made is 0")
	}
}
