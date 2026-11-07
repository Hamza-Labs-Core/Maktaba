//go:build race

package pluginflow

// raceOn: the race detector slows every memory access several times over, so N22's
// budget is timed only in the builds without it (the integration tier).
const raceOn = true
