package system

// DiskFreeBytes returns the bytes available to an unprivileged process
// on the filesystem holding path, or an error when the probe is
// unsupported (non-unix) or fails. It is the exported seam the
// diagnostics-export handler uses to report free disk in system-info,
// reusing the same platform-specific probe the health aggregator uses.
func DiskFreeBytes(path string) (uint64, error) { return diskFreeBytes(path) }
