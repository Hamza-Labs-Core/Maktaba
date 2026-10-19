//go:build windows

package system

import (
	"errors"
	"path/filepath"
	"testing"
)

// Stub audit #26: the Windows probe reports the real free space, so the
// self-update disk check runs instead of being skipped.
func TestDiskFreeBytesWindows(t *testing.T) {
	dir := t.TempDir()
	free, err := diskFreeBytes(dir)
	if err != nil || free == 0 {
		t.Fatalf("diskFreeBytes(%s) = %d, %v", dir, free, err)
	}
	// a relative path resolves against the working directory's volume
	if rel, err := diskFreeBytes("."); err != nil || rel == 0 {
		t.Fatalf("diskFreeBytes(.) = %d, %v", rel, err)
	}
	if _, err := diskFreeBytes(filepath.Join(dir, "missing", "deeper")); err == nil {
		t.Fatal("a missing directory should fail the probe")
	}
	if err := diskCheck(dir, 1); err != nil {
		t.Fatalf("1 byte should fit: %v", err)
	}
	if err := diskCheck(dir, int64(free)+(1<<40)); !errors.Is(err, errDiskSpace) {
		t.Fatalf("diskCheck over the free space = %v, want errDiskSpace", err)
	}
}
