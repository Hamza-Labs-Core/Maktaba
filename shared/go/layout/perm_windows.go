//go:build windows

package layout

// checkNotShared: Windows has no mode bits; the install root's ACL (the
// user's profile or %ProgramData% for a service) is what protects it.
func checkNotShared(string) error { return nil }
