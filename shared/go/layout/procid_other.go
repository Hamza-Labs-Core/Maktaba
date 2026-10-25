//go:build !linux && !darwin && !windows

package layout

// BootID is not known here: no handover record is ever taken for this boot's.
func BootID() string { return "" }

// ProcessStart is not known here: no process is ever adopted.
func ProcessStart(int) (uint64, error) { return 0, ErrNoProcess }
