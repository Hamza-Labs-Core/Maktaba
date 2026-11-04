package store

import "github.com/Hamza-Labs-Core/Maktaba/shared/components"

// rollupRank orders the phases worst first: the phase a component shows for a machine
// is the worst of its parts'. Installing and removing rank with waiting (the component
// is not usable while its files change).
var rollupRank = map[string]int{
	components.PhaseFailed:       0,
	components.PhaseWaiting:      1,
	components.PhaseRemoving:     2,
	components.PhaseInstalling:   3,
	components.PhaseDraining:     4,
	components.PhaseStarting:     5,
	components.PhaseDegraded:     6,
	components.PhaseStopped:      7,
	components.PhaseRunning:      8,
	components.PhaseOff:          9,
	components.PhaseNotInstalled: 10,
}

// Rollup is one component's status on one machine from its parts' rows: the worst
// phase wins, with that part's conditions and reason (Running in the API and Failed in
// the pipeline is Failed, with the pipeline's reason). The first of equally bad rows
// wins. Rows of other components or machines are the caller's to split first
// (RollupAll). An empty rows is the zero Status.
func Rollup(rows []Status) Status {
	if len(rows) == 0 {
		return Status{}
	}
	best := rows[0]
	for _, r := range rows[1:] {
		if rank(r.Phase) < rank(best.Phase) {
			best = r
		}
	}
	best.Part = ""
	return best
}

func rank(phase string) int {
	if r, ok := rollupRank[phase]; ok {
		return r
	}
	return -1 // a phase this release does not know (a newer one wrote it): worst
}

// Key names one component on one machine.
type Key struct{ Machine, ID string }

// RollupAll is Rollup of every (machine, component) in rows.
func RollupAll(rows []Status) map[Key]Status {
	groups := map[Key][]Status{}
	for _, r := range rows {
		k := Key{r.Machine, r.ID}
		groups[k] = append(groups[k], r)
	}
	out := make(map[Key]Status, len(groups))
	for k, g := range groups {
		out[k] = Rollup(g)
	}
	return out
}
