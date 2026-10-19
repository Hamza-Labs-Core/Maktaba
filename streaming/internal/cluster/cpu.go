package cluster

import "sync"

// cpuSampler reports the machine's CPU use (0..1) between two calls.
type cpuSampler struct {
	mu          sync.Mutex
	idle, total uint64
}

// Load returns the CPU use since the previous call (0 on the first call or when the OS
// gives nothing).
func (c *cpuSampler) Load() float64 {
	idle, total, ok := cpuTimes()
	if !ok {
		return loadFallback()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	di, dt := idle-c.idle, total-c.total
	first := c.total == 0
	c.idle, c.total = idle, total
	if first || dt == 0 || di > dt {
		return 0
	}
	u := 1 - float64(di)/float64(dt)
	if u < 0 {
		u = 0
	}
	if u > 1 {
		u = 1
	}
	return u
}

// NewCPUSampler returns a CPU-use sampler (Load: 0..1 since the previous call).
func NewCPUSampler() interface{ Load() float64 } { return &cpuSampler{} }
