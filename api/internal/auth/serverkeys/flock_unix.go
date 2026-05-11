//go:build unix

package serverkeys

import (
	"os"

	"golang.org/x/sys/unix"
)

func flockEx(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func flockUn(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
