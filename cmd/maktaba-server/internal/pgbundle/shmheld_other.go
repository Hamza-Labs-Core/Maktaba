//go:build !windows

package pgbundle

// ShmHeld reports whether the shared memory Postgres makes for the cluster in data is
// held by processes of a postmaster that is gone. On the unix systems it cannot be told
// without the postmaster's lock (the segment's id, which the system names by number), so
// it says false: the start's own wait, by the lock (AwaitShmFree), covers it there.
func ShmHeld(data string) bool { return false }
