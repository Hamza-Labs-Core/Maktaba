//go:build !unix

package serverkeys

import "os"

// On non-unix builds we fall back to advisory in-process locking
// only; the test suite skips multi-process race tests there.
func flockEx(*os.File) error { return nil }
func flockUn(*os.File) error { return nil }
