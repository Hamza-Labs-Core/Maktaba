package hostinfo

// What Maktaba knows about the machine it runs on (Epic 59 story 59.04): the memory it may
// use (a container's cgroup limit, not the host's RAM), the memory it can have now (with
// the ZFS cache counted as free), the CPU it may use and its features, the GPUs for video
// and speech, the kernel's sandbox features and the platform (a NAS's own system, Docker,
// plain Linux). The tiers (tier.go) are computed from it.
//
// Every reading here is made from files under a root ("/" on a real machine), so the tests
// drive it with fixture trees (testdata/). A file that cannot be read leaves its field at
// the zero value: nothing here fails.
//
// API for the other stories (59.02, 59.05, 59.06, 59.07):
//
//	Detect()              this machine, read once and cached for the process's life
//	DetectAt(Probe)       the same reading against a fixture root (tests)
//	DataDiskOf(dir)       the disk under a data folder: rotational, ssd or unknown (disk.go)
//	TierFor(limit, arch)  Light, Standard or Strong from the memory limit (tier.go)
//	DefaultsFor(tier, m)  what that tier sets: Postgres, workers, transcodes, speech, quiet hours
//	MemoryLimit()         the memory limit alone (cheap; the supervisor's planning uses it)

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Platforms Detect names (Machine.Platform).
const (
	PlatformDSM     = "dsm"     // Synology DSM
	PlatformTrueNAS = "truenas" // TrueNAS SCALE
	PlatformUnraid  = "unraid"  // Unraid
	PlatformQNAP    = "qts"     // QNAP QTS / QuTS hero (MAKTABA_PLATFORM only)
	PlatformOMV     = "omv"     // OpenMediaVault (MAKTABA_PLATFORM only)
	PlatformDocker  = "docker"  // a container on some other Linux
	PlatformLinux   = "linux"
)

// EnvPlatform names the platform outright; the NAS compose files set it (59.09–59.11).
const EnvPlatform = "MAKTABA_PLATFORM"

// IsNAS reports whether platform is a NAS's own system.
func IsNAS(platform string) bool {
	switch platform {
	case PlatformDSM, PlatformTrueNAS, PlatformUnraid, PlatformQNAP, PlatformOMV, "nas":
		return true
	}
	return false
}

// Machine is one reading of this machine.
type Machine struct {
	// Platform is dsm, truenas, unraid, qts, omv, docker or linux (on Linux), else
	// darwin or windows; PlatformFrom says how it was known: "env" (MAKTABA_PLATFORM)
	// or "detected".
	Platform     string `json:"platform"`
	PlatformFrom string `json:"platform_from"`
	// Arch is the processor's architecture as Go names it (amd64, arm64).
	Arch   string `json:"arch"`
	Memory Memory `json:"memory"`
	CPU    CPU    `json:"cpu"`
	GPU    GPUs   `json:"gpu"`
	Kernel Kernel `json:"kernel"`
}

// Memory is what memory Maktaba may use and has now, in bytes.
type Memory struct {
	// LimitBytes is the memory Maktaba may use: the cgroup's limit or the machine's
	// RAM, whichever is smaller. The tier follows it.
	LimitBytes uint64 `json:"limit_bytes"`
	// TotalBytes is the RAM the kernel sees (MemTotal).
	TotalBytes uint64 `json:"total_bytes"`
	// CgroupLimitBytes is the container's limit (0: none); CgroupVersion 1 or 2 (0: no
	// cgroup read).
	CgroupLimitBytes uint64 `json:"cgroup_limit_bytes,omitempty"`
	CgroupVersion    int    `json:"cgroup_version,omitempty"`
	// AvailableBytes is what a new process can have: MemAvailable plus what the ZFS
	// cache gives back (ARCReclaimableBytes), never above LimitBytes.
	AvailableBytes      uint64 `json:"available_bytes"`
	ARCReclaimableBytes uint64 `json:"arc_reclaimable_bytes,omitempty"`
	// ARCUnread: this machine has ZFS but its cache's numbers could not be read
	// (/proc/spl hidden), so available memory may look lower than it is.
	ARCUnread bool `json:"arc_unread,omitempty"`
}

// CPU is the processor Maktaba may use.
type CPU struct {
	// Cores is what Maktaba may use: the online CPUs, capped by the cgroup's quota
	// (Go's runtime.NumCPU does not read the quota). Online is the CPUs before the
	// quota; Quota the quota in CPUs (0: none).
	Cores  int     `json:"cores"`
	Online int     `json:"online"`
	Quota  float64 `json:"quota,omitempty"`
	Model  string  `json:"model,omitempty"`
	// AVX2 and AVX512F: the x86 vector extensions speech-to-text runs much faster with.
	AVX2    bool `json:"avx2"`
	AVX512F bool `json:"avx512f"`
}

// GPUs are the devices video and speech can use.
type GPUs struct {
	// Render are /dev/dri's render nodes (VA-API, Quick Sync).
	Render []RenderDevice `json:"render"`
	// NVIDIA are the NVIDIA GPUs nvidia-smi lists (only looked for when /dev/nvidia*
	// exists on Linux).
	NVIDIA []GPU `json:"nvidia"`
}

// RenderDevice is one /dev/dri render node and the kernel driver behind it (i915, xe,
// amdgpu, …; "" when /sys does not say).
type RenderDevice struct {
	Path   string `json:"path"`
	Driver string `json:"driver,omitempty"`
}

// Kernel is the kernel's version and the sandbox features plugin isolation uses (59.07).
type Kernel struct {
	Release string `json:"release,omitempty"`
	// Landlock is the Landlock ABI version (0: none).
	Landlock int `json:"landlock"`
	// UserNS: a child could be made in a new user namespace; nil when not tried.
	UserNS *bool `json:"userns,omitempty"`
}

// Uname is the uname fields platform detection reads.
type Uname struct {
	Release string
	Version string
	Machine string
}

// Probe is where a reading comes from: a root to read files under and the facts that
// are not files. Zero fields mean this machine's (System()).
type Probe struct {
	Root   string
	GOOS   string
	GOARCH string
	// Env reads an environment variable (MAKTABA_PLATFORM, MAKTABA_CONTAINER).
	Env func(string) string
	// NumCPU is the CPUs this process may run on (runtime.NumCPU).
	NumCPU int
	Uname  Uname
	// NVIDIA lists the NVIDIA GPUs (nvidia-smi); called only when /dev/nvidia* exists.
	NVIDIA func() []GPU
	// Landlock and UserNS probe the kernel (nil: not probed).
	Landlock func() int
	UserNS   func() *bool
}

// System is this machine's probe.
func System() Probe {
	return Probe{
		Root: "/", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Env: os.Getenv,
		NumCPU: runtime.NumCPU(), Uname: uname(), NVIDIA: NVIDIA,
		Landlock: landlockABI, UserNS: userNamespaces,
	}
}

var (
	detectOnce sync.Once
	detected   Machine
)

// Detect reads this machine once; later calls return the same reading (the tier is
// computed at each start, and memory added needs a restart to count anyway).
func Detect() Machine {
	detectOnce.Do(func() { detected = DetectAt(System()) })
	return detected
}

// MemoryLimit is the memory Maktaba may use (Machine.Memory.LimitBytes) without the rest
// of Detect: on Linux the cgroup limit or MemTotal, else the machine's RAM.
func MemoryLimit() uint64 {
	if runtime.GOOS != "linux" {
		return TotalMemory()
	}
	return readMemory(Probe{Root: "/"}, "").LimitBytes
}

// CPUCores is the CPUs Maktaba may use (Machine.CPU.Cores): runtime.NumCPU capped by a
// Linux cgroup's quota.
func CPUCores() int {
	if runtime.GOOS != "linux" {
		return runtime.NumCPU()
	}
	return readCPU(Probe{Root: "/", NumCPU: runtime.NumCPU()}).Cores
}

// DetectAt reads the machine p describes.
func DetectAt(p Probe) Machine {
	if p.Root == "" {
		p.Root = "/"
	}
	if p.Env == nil {
		p.Env = func(string) string { return "" }
	}
	m := Machine{Arch: p.GOARCH}
	m.Platform, m.PlatformFrom = platformOf(p)
	if p.GOOS != "linux" {
		m.Memory = Memory{LimitBytes: TotalMemory(), TotalBytes: TotalMemory(), AvailableBytes: FreeMemory()}
		m.CPU = CPU{Cores: p.NumCPU, Online: p.NumCPU}
		if p.GOOS != "darwin" && p.NVIDIA != nil {
			m.GPU.NVIDIA = p.NVIDIA()
		}
		m.GPU.Render = []RenderDevice{}
		if m.GPU.NVIDIA == nil {
			m.GPU.NVIDIA = []GPU{}
		}
		return m
	}
	m.Memory = readMemory(p, m.Platform)
	m.CPU = readCPU(p)
	m.GPU = readGPUs(p)
	m.Kernel.Release = p.Uname.Release
	if p.Landlock != nil {
		m.Kernel.Landlock = p.Landlock()
	}
	if p.UserNS != nil {
		m.Kernel.UserNS = p.UserNS()
	}
	return m
}

// at is name under the probe's root.
func (p Probe) at(name string) string { return filepath.Join(p.Root, filepath.FromSlash(name)) }

func (p Probe) read(name string) (string, bool) {
	b, err := os.ReadFile(p.at(name))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

func (p Probe) exists(name string) bool {
	_, err := os.Stat(p.at(name))
	return err == nil
}

// ---- platform ----

func platformOf(p Probe) (platform, from string) {
	if v := strings.ToLower(strings.TrimSpace(p.Env(EnvPlatform))); v != "" {
		return v, "env"
	}
	if p.GOOS != "linux" {
		return p.GOOS, "detected"
	}
	kernel := strings.ToLower(p.Uname.Release + " " + p.Uname.Version + " " + p.Uname.Machine)
	switch {
	case p.exists("/etc.defaults/VERSION") || strings.Contains(kernel, "synology"):
		return PlatformDSM, "detected"
	case strings.Contains(kernel, "truenas"):
		return PlatformTrueNAS, "detected"
	case p.exists("/etc/version") && looksTrueNAS(p):
		return PlatformTrueNAS, "detected"
	case p.exists("/etc/unraid-version") || strings.Contains(kernel, "unraid"):
		return PlatformUnraid, "detected"
	case p.Env("MAKTABA_CONTAINER") != "" || p.exists("/.dockerenv") || p.exists("/run/.containerenv"):
		return PlatformDocker, "detected"
	}
	return PlatformLinux, "detected"
}

// looksTrueNAS: TrueNAS SCALE's /etc/version is a bare version ("25.04.1"); another
// system's (rare) is not taken for it without TrueNAS's middleware beside it.
func looksTrueNAS(p Probe) bool {
	v, _ := p.read("/etc/version")
	if strings.Contains(strings.ToLower(v), "truenas") {
		return true
	}
	if p.exists("/usr/lib/python3/dist-packages/middlewared") || p.exists("/data/freenas-v1.db") {
		return true
	}
	// a version alone: digits and dots (and a suffix such as -MASTER)
	if v == "" || len(v) > 40 {
		return false
	}
	return v[0] >= '0' && v[0] <= '9' && strings.Count(v, ".") >= 1 && !strings.ContainsAny(v, " \n")
}

// ---- memory ----

// cgroupNone: a cgroup v1 limit at or above this is "no limit" (v1 writes one near 2^63,
// rounded down to the page size).
const cgroupNone = uint64(1) << 62

func readMemory(p Probe, platform string) Memory {
	info := meminfo(p)
	m := Memory{TotalBytes: info["MemTotal"]}
	m.CgroupLimitBytes, m.CgroupVersion = cgroupMemoryLimit(p)
	m.LimitBytes = m.TotalBytes
	if m.CgroupLimitBytes > 0 && (m.LimitBytes == 0 || m.CgroupLimitBytes < m.LimitBytes) {
		m.LimitBytes = m.CgroupLimitBytes
	}
	avail := info["MemAvailable"]
	if arc, ok := arcReclaimable(p); ok {
		m.ARCReclaimableBytes = arc
		avail += arc
	} else if platform == PlatformTrueNAS || hasZFS(p) {
		m.ARCUnread = true
	}
	if m.LimitBytes > 0 && avail > m.LimitBytes {
		avail = m.LimitBytes
	}
	m.AvailableBytes = avail
	return m
}

// meminfo is /proc/meminfo in bytes.
func meminfo(p Probe) map[string]uint64 {
	out := map[string]uint64{}
	s, _ := p.read("/proc/meminfo")
	for _, line := range strings.Split(s, "\n") {
		k, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && strings.EqualFold(f[1], "kB") {
			n <<= 10
		}
		out[strings.TrimSpace(k)] = n
	}
	return out
}

// cgroupPaths are this process's cgroup paths from /proc/self/cgroup: "" → the v2
// path, each v1 controller → its path.
func cgroupPaths(p Probe) map[string]string {
	out := map[string]string{}
	s, _ := p.read("/proc/self/cgroup")
	for _, line := range strings.Split(s, "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) != 3 {
			continue
		}
		if f[1] == "" {
			out[""] = f[2]
			continue
		}
		for _, c := range strings.Split(f[1], ",") {
			out[c] = f[2]
		}
	}
	return out
}

// cgroupFile reads name in the cgroup at path under base and in each of its ancestors (a
// limit set higher up applies too), calling each value found; a container with its own
// cgroup namespace sees its cgroup at base itself.
func cgroupFiles(p Probe, base, path, name string, each func(string)) {
	seen := map[string]bool{}
	for dir := filepath.Clean("/" + path); ; dir = filepath.Dir(dir) {
		f := filepath.Join(base, dir, name)
		if !seen[f] {
			seen[f] = true
			if v, ok := p.read(f); ok {
				each(v)
			}
		}
		if dir == "/" {
			break
		}
	}
	if f := filepath.Join(base, name); !seen[f] {
		if v, ok := p.read(f); ok {
			each(v)
		}
	}
}

// cgroupMemoryLimit is the smallest memory limit over this process's cgroup and its
// ancestors (0: none), and the cgroup version it was read from.
func cgroupMemoryLimit(p Probe) (uint64, int) {
	paths := cgroupPaths(p)
	var limit uint64
	version := 0
	take := func(v string, ver int) {
		if version == 0 {
			version = ver
		}
		if v == "max" || v == "" {
			return
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil || n == 0 || n >= cgroupNone {
			return
		}
		if limit == 0 || n < limit {
			limit = n
		}
	}
	if p.exists("/sys/fs/cgroup/cgroup.controllers") { // v2
		cgroupFiles(p, "/sys/fs/cgroup", paths[""], "memory.max", func(v string) { take(v, 2) })
		if version == 0 {
			version = 2
		}
		return limit, version
	}
	for _, dir := range []string{"/sys/fs/cgroup/memory", "/sys/fs/cgroup/memory,cpuacct"} {
		if p.exists(dir) {
			cgroupFiles(p, dir, paths["memory"], "memory.limit_in_bytes", func(v string) { take(v, 1) })
			break
		}
	}
	return limit, version
}

// arcReclaimable is what the ZFS cache gives back under pressure: its size above its
// floor (arcstats: size − c_min); false when arcstats cannot be read.
func arcReclaimable(p Probe) (uint64, bool) {
	s, ok := p.read("/proc/spl/kstat/zfs/arcstats")
	if !ok {
		return 0, false
	}
	vals := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 {
			if n, err := strconv.ParseUint(f[2], 10, 64); err == nil {
				vals[f[0]] = n
			}
		}
	}
	size, okSize := vals["size"]
	cmin, okMin := vals["c_min"]
	if !okSize || !okMin {
		return 0, false
	}
	if size <= cmin {
		return 0, true
	}
	return size - cmin, true
}

// hasZFS: a ZFS file system is mounted (this process's view).
func hasZFS(p Probe) bool {
	for _, mt := range readMountinfo(p) {
		if mt.FSType == "zfs" {
			return true
		}
	}
	return false
}

// ---- CPU ----

func readCPU(p Probe) CPU {
	c := CPU{Online: p.NumCPU}
	if c.Online <= 0 {
		c.Online = 1
	}
	c.Quota = cgroupCPUQuota(p)
	c.Cores = c.Online
	if c.Quota > 0 {
		q := int(c.Quota + 0.999) // a part of a CPU counts as one
		if q < 1 {
			q = 1
		}
		if q < c.Cores {
			c.Cores = q
		}
	}
	s, _ := p.read("/proc/cpuinfo")
	sc := bufio.NewScanner(bytes.NewBufferString(s))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "model name", "Model", "Hardware":
			if c.Model == "" {
				c.Model = v
			}
		case "flags":
			for _, f := range strings.Fields(v) {
				switch f {
				case "avx2":
					c.AVX2 = true
				case "avx512f":
					c.AVX512F = true
				}
			}
		}
	}
	return c
}

// cgroupCPUQuota is the CPUs the cgroup's quota allows (0: none): v2 cpu.max
// ("200000 100000" is 2), v1 cpu.cfs_quota_us / cpu.cfs_period_us.
func cgroupCPUQuota(p Probe) float64 {
	paths := cgroupPaths(p)
	quota := 0.0
	take := func(q float64) {
		if q > 0 && (quota == 0 || q < quota) {
			quota = q
		}
	}
	if p.exists("/sys/fs/cgroup/cgroup.controllers") {
		cgroupFiles(p, "/sys/fs/cgroup", paths[""], "cpu.max", func(v string) {
			f := strings.Fields(v)
			if len(f) != 2 || f[0] == "max" {
				return
			}
			q, err1 := strconv.ParseFloat(f[0], 64)
			per, err2 := strconv.ParseFloat(f[1], 64)
			if err1 == nil && err2 == nil && per > 0 {
				take(q / per)
			}
		})
		return quota
	}
	for _, dir := range []string{"/sys/fs/cgroup/cpu,cpuacct", "/sys/fs/cgroup/cpu"} {
		if !p.exists(dir) {
			continue
		}
		path := paths["cpu"]
		q, okQ := p.read(filepath.Join(dir, path, "cpu.cfs_quota_us"))
		per, okP := p.read(filepath.Join(dir, path, "cpu.cfs_period_us"))
		if !okQ || !okP {
			q, okQ = p.read(filepath.Join(dir, "cpu.cfs_quota_us"))
			per, okP = p.read(filepath.Join(dir, "cpu.cfs_period_us"))
		}
		if okQ && okP {
			qn, err1 := strconv.ParseFloat(q, 64)
			pn, err2 := strconv.ParseFloat(per, 64)
			if err1 == nil && err2 == nil && qn > 0 && pn > 0 {
				take(qn / pn)
			}
		}
		break
	}
	return quota
}

// ---- GPUs ----

func readGPUs(p Probe) GPUs {
	g := GPUs{Render: []RenderDevice{}, NVIDIA: []GPU{}}
	names, _ := filepath.Glob(p.at("/dev/dri/renderD*"))
	sort.Strings(names)
	for _, n := range names {
		base := filepath.Base(n)
		d := RenderDevice{Path: "/dev/dri/" + base}
		if target, err := os.Readlink(p.at("/sys/class/drm/" + base + "/device/driver")); err == nil {
			d.Driver = filepath.Base(target)
		} else if v, ok := p.read("/sys/class/drm/" + base + "/device/driver_name"); ok {
			d.Driver = v // fixtures without symlinks
		}
		g.Render = append(g.Render, d)
	}
	if nv, _ := filepath.Glob(p.at("/dev/nvidia[0-9]*")); len(nv) > 0 && p.NVIDIA != nil {
		if list := p.NVIDIA(); list != nil {
			g.NVIDIA = list
		}
	}
	return g
}
