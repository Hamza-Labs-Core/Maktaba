//go:build !unix

package pluginflow

import "time"

// cpuTime is not read here: the budget is timed by the wall clock.
func cpuTime() time.Duration { return 0 }
