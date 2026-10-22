//go:build windows

package cache

import "golang.org/x/sys/windows"

// DiskSpace is the space free to this process on path's volume, and the
// volume's size.
func DiskSpace(path string) (free, total uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, 0, err
	}
	return free, total, nil
}
