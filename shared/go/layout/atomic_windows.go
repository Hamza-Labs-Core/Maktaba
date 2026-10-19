//go:build windows

package layout

import "golang.org/x/sys/windows"

// replaceFile renames with MOVEFILE_WRITE_THROUGH so the call returns only
// once the rename is on disk (Windows has no directory fsync).
func replaceFile(from, to string) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func syncDir(string) error { return nil }
