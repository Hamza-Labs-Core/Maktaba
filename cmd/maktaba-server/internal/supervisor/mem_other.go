//go:build !darwin

package supervisor

// totalMemory is only read on Apple Silicon (PlanSTT).
var totalMemory = func() uint64 { return 0 }
