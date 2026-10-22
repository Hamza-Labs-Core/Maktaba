//go:build !windows

package nodecopy

// restrictACL is Windows only: on Unix the modes do it.
func restrictACL(string) error { return nil }
