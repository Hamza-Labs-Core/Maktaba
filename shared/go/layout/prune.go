package layout

import "os"

// MinKeep is the floor for keep_versions: current + previous.
const MinKeep = 2

// PruneResult lists what Prune did.
type PruneResult struct {
	Removed []string
	Kept    []string
	// Skipped could not be removed (Windows: a running .exe is locked, EC3);
	// they are retried at the next prune.
	Skipped []string
}

// Prune removes old release directories, keeping the newest keep versions
// (never fewer than MinKeep) and always current, previous, pending, and every
// version in inUse (the front door, a TV slot: story 35.2 AC11).
func (l Layout) Prune(keep int, inUse ...string) (PruneResult, error) {
	if keep < MinKeep {
		keep = MinKeep
	}
	vs, err := l.Versions()
	if err != nil {
		return PruneResult{}, err
	}
	protect := map[string]bool{}
	for _, v := range inUse {
		protect[NormalizeVersion(v)] = true
	}
	if v, err := l.Current(); err == nil {
		protect[v] = true
	}
	if v, err := l.Previous(); err == nil {
		protect[v] = true
	}
	if p, _ := l.ReadPending(); p != nil {
		protect[p.Version] = true
		if p.Previous != "" {
			protect[p.Previous] = true
		}
	}
	var res PruneResult
	kept := 0
	for i := len(vs) - 1; i >= 0; i-- { // newest first: the first `keep` survive
		v := vs[i]
		if protect[v] || kept < keep {
			res.Kept = append(res.Kept, v)
			kept++
			continue
		}
		if err := os.RemoveAll(l.ReleaseDir(v)); err != nil {
			res.Skipped = append(res.Skipped, v)
			continue
		}
		res.Removed = append(res.Removed, v)
	}
	return res, nil
}
