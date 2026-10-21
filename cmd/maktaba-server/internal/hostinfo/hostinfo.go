// Package hostinfo reads what the desktop app's setup checks about this
// machine (Epic 38 story 38.6, the `check` step): its memory and the free
// space of the disk the install's state goes on.
package hostinfo

import (
	"os"
	"path/filepath"
)

// FreeDisk is the space free to this user on the disk that holds path, or
// would (the nearest folder that exists); 0 when it cannot be read.
func FreeDisk(path string) uint64 {
	for p := filepath.Clean(path); ; {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			n, err := freeDisk(p)
			if err != nil {
				return 0
			}
			return n
		}
		up := filepath.Dir(p)
		if up == p {
			return 0
		}
		p = up
	}
}
