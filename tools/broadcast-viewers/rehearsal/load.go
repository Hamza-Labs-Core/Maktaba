package rehearsal

// The fan-out's load signals (story 50.29): what each part of the rehearsal used of the runner
// over a step, so a slow fetch can be put down to the edge's own answer, the edge process, the
// home's encoder, the viewers' load generator or the runner's host (steal). Parsed here, without
// Docker, so they are unit-tested (T02); stack_test.go reads them.

import (
	"bufio"
	"math"
	"strconv"
	"strings"
)

// SegAnswerBoundsMS are the upper bounds (ms) of the edge's segment answer-time buckets, the
// same as SEG_ANSWER_BOUNDS_MS in tools/rehearsal-cloud/broadcast/edge/entry.ts; the counters
// carry one more bucket, for longer answers.
var SegAnswerBoundsMS = []float64{5, 10, 25, 50, 100, 250, 500, 1000, 1500, 2500, 5000}

// BucketQuantile is the upper bound of the bucket holding the q-th (0..1) sample of counts
// (bucket i holds samples ≤ bounds[i]; the one past the bounds, the longer ones, reads +Inf).
// No sample: 0.
func BucketQuantile(counts []int, bounds []float64, q float64) float64 {
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return 0
	}
	rank := int(math.Ceil(q * float64(total)))
	rank = max(rank, 1)
	seen := 0
	for i, c := range counts {
		seen += c
		if seen >= rank {
			if i < len(bounds) {
				return bounds[i]
			}
			return math.Inf(1)
		}
	}
	return math.Inf(1)
}

// CgroupCPUUsec is usage_usec from a cgroup v2 cpu.stat (a container's CPU time so far).
func CgroupCPUUsec(cpuStat string) (int64, bool) {
	sc := bufio.NewScanner(strings.NewReader(cpuStat))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[0] == "usage_usec" {
			n, err := strconv.ParseInt(f[1], 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// HostCPU is the aggregate "cpu" line of /proc/stat, in clock ticks.
type HostCPU struct {
	Total, Idle, Steal int64
}

// ParseProcStat reads /proc/stat's first line: user nice system idle iowait irq softirq steal.
func ParseProcStat(stat string) (HostCPU, bool) {
	line, _, _ := strings.Cut(stat, "\n")
	f := strings.Fields(line)
	if len(f) < 9 || f[0] != "cpu" {
		return HostCPU{}, false
	}
	var h HostCPU
	for i, s := range f[1:] {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return HostCPU{}, false
		}
		// guest and guest_nice (9th, 10th) are already counted in user and nice
		if i < 8 {
			h.Total += n
		}
		switch i {
		case 3, 4: // idle, iowait
			h.Idle += n
		case 7:
			h.Steal = n
		}
	}
	return h, true
}

// HostShare is the runner's busy and stolen share (percent of every CPU) between two readings.
func HostShare(a, b HostCPU) (busy, steal float64) {
	d := b.Total - a.Total
	if d <= 0 {
		return 0, 0
	}
	return 100 * float64(d-(b.Idle-a.Idle)) / float64(d), 100 * float64(b.Steal-a.Steal) / float64(d)
}

// Cores is CPU time over a wall time, in cores (1.0: one CPU busy the whole time).
func Cores(cpuUsec int64, wallSeconds float64) float64 {
	if wallSeconds <= 0 {
		return 0
	}
	return math.Round(float64(cpuUsec)/1e6/wallSeconds*100) / 100
}
