//go:build !unix && !windows

package serverkeys

import "os"

// Platforms with neither flock nor LockFileEx (js/wasm, plan9) have no
// cross-process file lock; Maktaba servers do not ship there, so only
// the in-process sync.Once in lockDir orders key generation.
func flockEx(*os.File) error { return nil }
func flockUn(*os.File) error { return nil }
