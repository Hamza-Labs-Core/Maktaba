package hostinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The memory a new process can have is read, and is part of the RAM; this test's own
// resident memory is read by its pid, and a pid that is gone has none.
func TestFreeMemoryAndResidentMemory(t *testing.T) {
	free := FreeMemory()
	if free == 0 || free > TotalMemory() {
		t.Fatalf("FreeMemory = %d of %d", free, TotalMemory())
	}
	got := ProcessRSS([]int{os.Getpid(), 1 << 30})
	if got[os.Getpid()] < 1<<20 {
		t.Fatalf("ProcessRSS of this test = %d, want at least 1 MB", got[os.Getpid()])
	}
	if _, has := got[1<<30]; has {
		t.Fatal("a pid that does not exist has a resident memory")
	}
}

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

// Epic 53 story 53.12: the load average the Overview charts is a number of runnable
// processes: never negative, and read on the systems that keep one.
func TestLoadAverage(t *testing.T) {
	v, ok := LoadAverage()
	if (runtime.GOOS == "darwin" || runtime.GOOS == "linux") && !ok {
		t.Fatal("no load average on a system that keeps one")
	}
	if v < 0 || v > 10000 {
		t.Fatalf("load average %v", v)
	}
}
