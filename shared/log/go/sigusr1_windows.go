//go:build windows

package log

// installSIGUSR1 is a no-op on Windows: the signal does not exist on
// that platform. Operators flip the level via the admin endpoint
// instead (Story 23 wires the route).
func installSIGUSR1() {}
