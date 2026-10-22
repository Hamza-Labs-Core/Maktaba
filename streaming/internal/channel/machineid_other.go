//go:build !windows

package channel

// machineGUID: Windows' id of this PC; none here.
func machineGUID() string { return "" }
