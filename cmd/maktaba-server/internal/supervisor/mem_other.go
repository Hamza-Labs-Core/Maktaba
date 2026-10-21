//go:build !darwin && !linux && !windows

package supervisor

// totalMemory is unknown here: PlanSTT leaves the memory knobs to the pipeline.
var totalMemory = func() uint64 { return 0 }
