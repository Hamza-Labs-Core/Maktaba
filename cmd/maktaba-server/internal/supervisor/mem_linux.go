//go:build linux

package supervisor

import "github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"

// totalMemory is the memory this machine's processes may use, in bytes (0 when unknown):
// a container's cgroup limit when it is below the RAM (Epic 59 story 59.04), so a NAS
// container limited to 4 GB is planned for 4 GB, not the host's RAM.
var totalMemory = hostinfo.MemoryLimit
