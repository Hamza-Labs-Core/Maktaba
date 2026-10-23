//go:build windows

package layout

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

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
	// another process reading the file a moment (a reader opened without
	// FILE_SHARE_DELETE, an antivirus scan) refuses the replace: try again briefly
	wait := 5 * time.Millisecond
	for i := 0; ; i++ {
		err = windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil || i == 12 || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) &&
			!errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
			return err
		}
		time.Sleep(wait)
		wait = min(2*wait, 200*time.Millisecond)
	}
}

func syncDir(string) error { return nil }
