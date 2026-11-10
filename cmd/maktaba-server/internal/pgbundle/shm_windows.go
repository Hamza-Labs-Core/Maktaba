//go:build windows

package pgbundle

// shmInUse: Windows's postmaster holds a named mapping the system frees with its last
// handle; Postgres checks it itself (no lock is removed over a live one: its PID runs).
func shmInUse(string) bool { return false }
