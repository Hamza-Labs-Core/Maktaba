//go:build unix

package devmigrate

import "golang.org/x/sys/unix"

// freeBytes is the space free for this user on p's volume (-1 unknown).
func freeBytes(p string) int64 {
	var st unix.Statfs_t
	if unix.Statfs(p, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize) //nolint:unconvert // Bsize is uint32 on darwin, int64 on linux
}
