//go:build windows

package pgbundle

// ShmHeld reports whether the shared memory Postgres makes for the cluster in data is
// held by processes of a postmaster that is gone: on Windows the mapping is named after
// the directory, so it can be asked without the postmaster's lock — which a start that
// failed took with it. Postgres refuses to start beside it.
func ShmHeld(data string) bool {
	for _, d := range shmDataDirs(data) {
		name, err := utf16Ptr(shmName(d))
		if err != nil {
			continue
		}
		if mappingThere(name) {
			return true
		}
	}
	return false
}
