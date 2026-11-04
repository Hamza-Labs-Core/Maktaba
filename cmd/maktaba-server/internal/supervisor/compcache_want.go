package supervisor

import "github.com/Hamza-Labs-Core/Maktaba/shared/components/store"

// CachedDesired is the last desired state the reconciler saw for machine (its cache
// under stateDir), or ok false when there is none for it (story 51.17: the venv gate
// reads it before the database is up).
func CachedDesired(stateDir, machine string) (map[string]store.Desired, bool) {
	d, _, ok, err := readCompCache(CompCachePath(stateDir), machine)
	if err != nil || !ok {
		return nil, false
	}
	return d, true
}
