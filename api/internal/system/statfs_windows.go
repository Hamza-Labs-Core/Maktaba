//go:build windows

package system

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// diskFreeBytes returns the bytes available to the calling user on the
// volume holding path (GetDiskFreeSpaceExW's FreeBytesAvailableToCaller,
// which honours per-user quotas, the Windows counterpart of Bavail). A
// Windows home runs the live install, so System Health and the
// self-update disk check need the real figure.
func diskFreeBytes(path string) (uint64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	p, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, err
	}
	return avail, nil
}
