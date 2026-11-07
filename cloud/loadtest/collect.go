package loadtest

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParseProm reads Prometheus text: each series (its name and labels as written) to its value.
func ParseProm(r io.Reader) map[string]float64 {
	out := map[string]float64{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i < 0 {
			continue
		}
		v, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			continue
		}
		out[line[:i]] = v
	}
	return out
}

// Sum adds every series whose name is name (any labels) and whose labels contain each of with.
func Sum(m map[string]float64, name string, with ...string) float64 {
	total := 0.0
	for k, v := range m {
		if k != name && !strings.HasPrefix(k, name+"{") {
			continue
		}
		ok := true
		for _, w := range with {
			if !strings.Contains(k, w) {
				ok = false
				break
			}
		}
		if ok {
			total += v
		}
	}
	return total
}

// Scrape reads a /metrics page.
func Scrape(ctx context.Context, url string) (map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	return ParseProm(resp.Body), nil
}

// Machine is a look at the node: CPU and memory as shares of the machine.
type Machine struct {
	CPU, Mem float64
	MemBytes float64
}

// NodeExporter reads a node_exporter between two scrapes: the busy share of all CPUs, and the
// memory not available (the staging pair, story 50.14 §3).
type NodeExporter struct {
	URL       string
	lastBusy  float64
	lastTotal float64
}

// Look scrapes once; the first look has no CPU yet (it needs two).
func (n *NodeExporter) Look(ctx context.Context) (Machine, error) {
	m, err := Scrape(ctx, n.URL)
	if err != nil {
		return Machine{}, err
	}
	total := Sum(m, "node_cpu_seconds_total")
	idle := Sum(m, "node_cpu_seconds_total", `mode="idle"`) + Sum(m, "node_cpu_seconds_total", `mode="iowait"`)
	busy := total - idle
	out := Machine{}
	if n.lastTotal > 0 && total > n.lastTotal {
		out.CPU = (busy - n.lastBusy) / (total - n.lastTotal)
	}
	n.lastBusy, n.lastTotal = busy, total
	if t := Sum(m, "node_memory_MemTotal_bytes"); t > 0 {
		used := t - Sum(m, "node_memory_MemAvailable_bytes")
		out.Mem, out.MemBytes = used/t, used
	}
	return out, nil
}

// Docker reads a container with `docker stats` (the rehearsal's one-node project in CI): its
// CPU as a share of the machine, its memory in bytes and as a share.
type Docker struct {
	Container string
	cpus      float64
}

// Look runs `docker stats` once.
func (d *Docker) Look(ctx context.Context) (Machine, error) {
	if d.cpus == 0 {
		out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.NCPU}}").Output()
		if err != nil {
			return Machine{}, fmt.Errorf("docker info: %w", err)
		}
		d.cpus, _ = strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
		if d.cpus <= 0 {
			d.cpus = 1
		}
	}
	out, err := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.CPUPerc}};{{.MemUsage}};{{.MemPerc}}", d.Container).Output()
	if err != nil {
		return Machine{}, fmt.Errorf("docker stats %s: %w", d.Container, err)
	}
	return ParseDockerStats(strings.TrimSpace(string(out)), d.cpus)
}

// ParseDockerStats reads one `docker stats` line ("143.2%;61.5MiB / 7.6GiB;0.79%"). Docker's
// CPU% counts each CPU as 100 %, so it is divided by the machine's CPUs.
func ParseDockerStats(line string, cpus float64) (Machine, error) {
	parts := strings.Split(line, ";")
	if len(parts) != 3 {
		return Machine{}, fmt.Errorf("docker stats: %q", line)
	}
	cpu, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(parts[0]), "%"), 64)
	if err != nil {
		return Machine{}, fmt.Errorf("docker stats cpu: %q", parts[0])
	}
	mem, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(parts[2]), "%"), 64)
	if err != nil {
		return Machine{}, fmt.Errorf("docker stats memory: %q", parts[2])
	}
	used, err := parseBytes(strings.TrimSpace(strings.SplitN(parts[1], "/", 2)[0]))
	if err != nil {
		return Machine{}, err
	}
	return Machine{CPU: cpu / 100 / cpus, Mem: mem / 100, MemBytes: used}, nil
}

func parseBytes(s string) (float64, error) {
	units := []struct {
		suffix string
		mult   float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
			if err != nil {
				return 0, fmt.Errorf("bytes %q", s)
			}
			return v * u.mult, nil
		}
	}
	return 0, fmt.Errorf("bytes %q", s)
}

// Looker is a Docker or a NodeExporter.
type Looker interface {
	Look(ctx context.Context) (Machine, error)
}

// Meter integrates a Looker's CPU over time (CPU-seconds of the machine's share × its CPUs)
// and keeps the highest memory: what load-small turns into its two numbers.
type Meter struct {
	Looker  Looker
	CPUs    float64
	cpuSecs float64
	peakMem float64
	last    time.Time
	Samples []Machine
}

// Tick takes one look at t; the time since the last look is charged at this look's CPU.
func (m *Meter) Tick(ctx context.Context, t time.Time) (Machine, error) {
	got, err := m.Looker.Look(ctx)
	if err != nil {
		return got, err
	}
	m.add(got, t)
	return got, nil
}

func (m *Meter) add(got Machine, t time.Time) {
	if !m.last.IsZero() {
		m.cpuSecs += got.CPU * m.CPUs * t.Sub(m.last).Seconds()
	}
	m.last = t
	if got.MemBytes > m.peakMem {
		m.peakMem = got.MemBytes
	}
	m.Samples = append(m.Samples, got)
}

// CPUSeconds and PeakMem are what the meter saw.
func (m *Meter) CPUSeconds() float64 { return m.cpuSecs }
func (m *Meter) PeakMem() float64    { return m.peakMem }

// CgroupCPU reads a container's own CPU time, the total its cgroup has charged since it started
// (cgroup v2 cpu.stat usage_usec, else v1 cpuacct.usage): the relay process's CPU-seconds,
// whatever else the machine runs. A sampled share (Docker) integrates instants and swings
// with what else is busy; two reads of this counter give the exact CPU used between them.
type CgroupCPU struct {
	Container string
}

// Seconds is the container's CPU time so far.
func (c CgroupCPU) Seconds(ctx context.Context) (float64, error) {
	out, err := exec.CommandContext(ctx, "docker", "exec", c.Container, "cat", "/sys/fs/cgroup/cpu.stat").Output()
	if err == nil {
		return ParseCPUStat(string(out))
	}
	out, err2 := exec.CommandContext(ctx, "docker", "exec", c.Container, "cat", "/sys/fs/cgroup/cpuacct/cpuacct.usage").Output()
	if err2 != nil {
		return 0, fmt.Errorf("the cgroup's CPU time of %s: %w (v1: %v)", c.Container, err, err2)
	}
	ns, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("cpuacct.usage %q", out)
	}
	return ns / 1e9, nil
}

// ParseCPUStat reads usage_usec from a cgroup v2 cpu.stat, in seconds.
func ParseCPUStat(s string) (float64, error) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "usage_usec" {
			us, err := strconv.ParseFloat(f[1], 64)
			if err != nil {
				return 0, fmt.Errorf("usage_usec %q", f[1])
			}
			return us / 1e6, nil
		}
	}
	return 0, fmt.Errorf("no usage_usec in cpu.stat")
}

// Median is the middle of xs (the mean of the two middle ones for an even count); 0 for none.
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
