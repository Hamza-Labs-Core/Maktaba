package hostinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a fake machine's root: path → contents ("->target" makes a symlink).
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if target, ok := strings.CutPrefix(body, "->"); ok {
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func meminfoKB(totalGiB, availGiB float64) string {
	kb := func(g float64) string { return itoa(int64(g * 1024 * 1024)) }
	return "MemTotal:       " + kb(totalGiB) + " kB\nMemFree:         1000 kB\nMemAvailable:   " + kb(availGiB) + " kB\n"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func linuxProbe(root string) Probe {
	return Probe{Root: root, GOOS: "linux", GOARCH: "amd64", NumCPU: 4, Env: func(string) string { return "" }}
}

// T01: a cgroup v2 limit of 6 GiB on a 32 GB host is the limit; the tier is Standard.
func TestCgroupV2Limit(t *testing.T) {
	root := fixture(t, map[string]string{
		"proc/meminfo":                     meminfoKB(31.3, 20),
		"proc/self/cgroup":                 "0::/\n",
		"sys/fs/cgroup/cgroup.controllers": "cpu memory\n",
		"sys/fs/cgroup/memory.max":         "6442450944\n",
	})
	m := DetectAt(linuxProbe(root))
	if m.Memory.LimitBytes != 6*GiB || m.Memory.CgroupVersion != 2 {
		t.Fatalf("memory = %+v, want a 6 GiB limit from cgroup v2", m.Memory)
	}
	if m.Memory.AvailableBytes > m.Memory.LimitBytes {
		t.Fatalf("available %d above the limit %d", m.Memory.AvailableBytes, m.Memory.LimitBytes)
	}
	if got := TierFor(m.Memory.LimitBytes, m.Arch); got != TierStandard {
		t.Fatalf("tier = %s, want standard", got)
	}
	if r := ReasonFor(TierStandard, m).String(); r != "Standard: 6 GB, no GPU for video" {
		t.Fatalf("reason = %q", r)
	}
}

// memory.max = max: no limit, MemTotal counts; a limit on an ancestor cgroup counts.
func TestCgroupV2NoLimitAndAncestor(t *testing.T) {
	root := fixture(t, map[string]string{
		"proc/meminfo":                        meminfoKB(8, 4),
		"proc/self/cgroup":                    "0::/docker/abc\n",
		"sys/fs/cgroup/cgroup.controllers":    "cpu memory\n",
		"sys/fs/cgroup/docker/abc/memory.max": "max\n",
	})
	m := DetectAt(linuxProbe(root))
	if m.Memory.LimitBytes != 8*GiB || m.Memory.CgroupLimitBytes != 0 {
		t.Fatalf("no limit: %+v", m.Memory)
	}
	if err := os.WriteFile(filepath.Join(root, "sys/fs/cgroup/docker/memory.max"), []byte("4294967296\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := DetectAt(linuxProbe(root)); m.Memory.LimitBytes != 4*GiB {
		t.Fatalf("a parent's limit: %+v", m.Memory)
	}
}

// T02 and T10: cgroup v1's limit is read; its "no limit" (near 2^63) is none.
func TestCgroupV1(t *testing.T) {
	root := fixture(t, map[string]string{
		"proc/meminfo":     meminfoKB(16, 10),
		"proc/self/cgroup": "11:memory:/docker/abc\n4:cpu,cpuacct:/docker/abc\n",
		"sys/fs/cgroup/memory/docker/abc/memory.limit_in_bytes": "4294967296\n",
	})
	m := DetectAt(linuxProbe(root))
	if m.Memory.LimitBytes != 4*GiB || m.Memory.CgroupVersion != 1 {
		t.Fatalf("v1: %+v", m.Memory)
	}
	if err := os.WriteFile(filepath.Join(root, "sys/fs/cgroup/memory/docker/abc/memory.limit_in_bytes"),
		[]byte("9223372036854771712\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := DetectAt(linuxProbe(root)); m.Memory.LimitBytes != 16*GiB || m.Memory.CgroupLimitBytes != 0 {
		t.Fatalf("v1 no limit: %+v", m.Memory)
	}
}

// T03: TrueNAS with 30 GB of ARC above a 1 GB floor and 1 GB MemAvailable shows about
// 30 GB available; without arcstats it says the cache was not read.
func TestZFSARC(t *testing.T) {
	arc := "13 1 0x01 123 33456 1234 5678\nname                            type data\nhits                            4    123\nsize                            4    " +
		itoa(31<<30) + "\nc_min                           4    " + itoa(1<<30) + "\nc_max                           4    " + itoa(31<<30) + "\n"
	root := fixture(t, map[string]string{
		"proc/meminfo":                meminfoKB(32, 1),
		"proc/spl/kstat/zfs/arcstats": arc,
		"proc/self/mountinfo":         "22 1 0:21 / / rw - zfs boot-pool/ROOT rw\n",
	})
	p := linuxProbe(root)
	p.Env = func(k string) string {
		if k == EnvPlatform {
			return "truenas"
		}
		return ""
	}
	m := DetectAt(p)
	if got := GB(m.Memory.AvailableBytes); got < 30 || got > 31.5 {
		t.Fatalf("available = %v GB (%+v), want about 31", got, m.Memory)
	}
	if m.Memory.ARCReclaimableBytes != 30<<30 || m.Memory.ARCUnread {
		t.Fatalf("arc: %+v", m.Memory)
	}
	if err := os.Remove(filepath.Join(root, "proc/spl/kstat/zfs/arcstats")); err != nil {
		t.Fatal(err)
	}
	m = DetectAt(p)
	if !m.Memory.ARCUnread || GB(m.Memory.AvailableBytes) != 1 {
		t.Fatalf("arcstats hidden: %+v", m.Memory)
	}
}

// T04: a J4125 has no AVX2, and on Standard speech picks base.
func TestCPUFlagsJ4125(t *testing.T) {
	cpu := "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Celeron(R) J4125 CPU @ 2.00GHz\n" +
		"flags\t\t: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush dts acpi mmx fxsr sse sse2 ss ht tm pbe syscall nx pdpe1gb rdtscp lm constant_tsc art arch_perfmon pebs bts rep_good nopl xtopology nonstop_tsc cpuid aperfmperf tsc_known_freq pni pclmulqdq dtes64 monitor ds_cpl vmx est tm2 ssse3 sdbg cx16 xtpr pdcm sse4_1 sse4_2 x2apic movbe popcnt tsc_deadline_timer aes xsave rdrand lahf_lm 3dnowprefetch cpuid_fault cat_l2 pti cdp_l2 ssbd ibrs ibpb stibp tpr_shadow vnmi flexpriority ept vpid ept_ad fsgsbase tsc_adjust smep erms mpx rdt_a rdseed smap clflushopt intel_pt sha_ni xsaveopt xsavec xgetbv1 xsaves dtherm ida arat pln pts md_clear arch_capabilities\n\n"
	root := fixture(t, map[string]string{
		"proc/meminfo":                           meminfoKB(6, 4),
		"proc/cpuinfo":                           strings.Repeat(cpu, 4),
		"dev/dri/renderD128":                     "",
		"sys/class/drm/renderD128/device/driver": "->../../../../bus/pci/drivers/i915",
	})
	m := DetectAt(linuxProbe(root))
	if m.CPU.AVX2 || m.CPU.AVX512F || !strings.Contains(m.CPU.Model, "J4125") {
		t.Fatalf("cpu: %+v", m.CPU)
	}
	tier := TierFor(m.Memory.LimitBytes, m.Arch)
	d := DefaultsFor(tier, m)
	if tier != TierStandard || d.SpeechModel != ModelBase || d.HardwareTranscodes != 4 {
		t.Fatalf("J4125 6 GB with a render device: %s %+v", tier, d)
	}
	if len(m.GPU.Render) != 1 || m.GPU.Render[0].Driver != "i915" {
		t.Fatalf("render: %+v", m.GPU.Render)
	}
	if r := ReasonFor(tier, m).String(); r != "Standard: 6 GB, Intel graphics" {
		t.Fatalf("reason = %q", r)
	}
}

// T05: render devices and their drivers (i915 and amdgpu); /dev/nvidia* asks nvidia-smi.
func TestRenderDevicesAndNVIDIA(t *testing.T) {
	root := fixture(t, map[string]string{
		"dev/dri/renderD128":                     "",
		"dev/dri/renderD129":                     "",
		"dev/dri/card0":                          "",
		"sys/class/drm/renderD128/device/driver": "->../../../bus/pci/drivers/i915",
		"sys/class/drm/renderD129/device/driver": "->../../../bus/pci/drivers/amdgpu",
		"dev/nvidia0":                            "",
	})
	p := linuxProbe(root)
	p.NVIDIA = func() []GPU { return []GPU{{Name: "NVIDIA GeForce RTX 3060", MemoryMB: 12288}} }
	m := DetectAt(p)
	want := []RenderDevice{{"/dev/dri/renderD128", "i915"}, {"/dev/dri/renderD129", "amdgpu"}}
	if len(m.GPU.Render) != 2 || m.GPU.Render[0] != want[0] || m.GPU.Render[1] != want[1] {
		t.Fatalf("render = %+v", m.GPU.Render)
	}
	if len(m.GPU.NVIDIA) != 1 || m.CUDAMiB() != 12288 {
		t.Fatalf("nvidia = %+v", m.GPU.NVIDIA)
	}
	// without /dev/nvidia*, nvidia-smi is not asked
	_ = os.Remove(filepath.Join(root, "dev/nvidia0"))
	p.NVIDIA = func() []GPU { t.Fatal("nvidia-smi asked without /dev/nvidia*"); return nil }
	if m := DetectAt(p); len(m.GPU.NVIDIA) != 0 {
		t.Fatalf("nvidia without a device: %+v", m.GPU.NVIDIA)
	}
}

// T06: DSM, TrueNAS and Unraid from their files or kernel strings; MAKTABA_PLATFORM wins.
func TestPlatform(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		uname Uname
		env   map[string]string
		want  string
	}{
		{"dsm file", map[string]string{"etc.defaults/VERSION": `majorversion="7"`}, Uname{}, nil, PlatformDSM},
		{"dsm kernel", map[string]string{"x": ""}, Uname{Release: "4.4.302+", Version: "#72806 SMP synology_geminilake_920+"}, nil, PlatformDSM},
		{"truenas file", map[string]string{"etc/version": "25.04.1"}, Uname{}, nil, PlatformTrueNAS},
		{"truenas kernel", map[string]string{"x": ""}, Uname{Release: "6.12.15-production+truenas"}, nil, PlatformTrueNAS},
		{"unraid", map[string]string{"etc/unraid-version": `version="7.0.1"`}, Uname{}, nil, PlatformUnraid},
		{"docker", map[string]string{".dockerenv": ""}, Uname{Release: "6.8.0-45-generic"}, nil, PlatformDocker},
		{"image", map[string]string{"x": ""}, Uname{}, map[string]string{"MAKTABA_CONTAINER": "docker"}, PlatformDocker},
		{"linux", map[string]string{"etc/version": "some words here"}, Uname{Release: "6.8.0"}, nil, PlatformLinux},
		{"env", map[string]string{"etc.defaults/VERSION": ""}, Uname{}, map[string]string{EnvPlatform: "TrueNAS"}, PlatformTrueNAS},
	}
	for _, c := range cases {
		p := linuxProbe(fixture(t, c.files))
		p.Uname = c.uname
		p.Env = func(k string) string { return c.env[k] }
		m := DetectAt(p)
		if m.Platform != c.want {
			t.Errorf("%s: platform %q, want %q", c.name, m.Platform, c.want)
		}
		if (c.env[EnvPlatform] != "") != (m.PlatformFrom == "env") {
			t.Errorf("%s: from %q", c.name, m.PlatformFrom)
		}
	}
	if !IsNAS(PlatformDSM) || !IsNAS(PlatformUnraid) || IsNAS(PlatformDocker) || IsNAS(PlatformLinux) {
		t.Fatal("IsNAS")
	}
}

// T11: cpu.max 200000 100000 on 4 CPUs is 2 cores; v1's quota too; no quota leaves them.
func TestCPUQuota(t *testing.T) {
	root := fixture(t, map[string]string{
		"proc/self/cgroup":                 "0::/\n",
		"sys/fs/cgroup/cgroup.controllers": "cpu memory\n",
		"sys/fs/cgroup/cpu.max":            "200000 100000\n",
	})
	if c := DetectAt(linuxProbe(root)).CPU; c.Cores != 2 || c.Online != 4 || c.Quota != 2 {
		t.Fatalf("v2 quota: %+v", c)
	}
	_ = os.WriteFile(filepath.Join(root, "sys/fs/cgroup/cpu.max"), []byte("150000 100000\n"), 0o644)
	if c := DetectAt(linuxProbe(root)).CPU; c.Cores != 2 {
		t.Fatalf("1.5 CPUs round up: %+v", c)
	}
	_ = os.WriteFile(filepath.Join(root, "sys/fs/cgroup/cpu.max"), []byte("max 100000\n"), 0o644)
	if c := DetectAt(linuxProbe(root)).CPU; c.Cores != 4 || c.Quota != 0 {
		t.Fatalf("no quota: %+v", c)
	}
	v1 := fixture(t, map[string]string{
		"proc/self/cgroup": "4:cpu,cpuacct:/docker/x\n",
		"sys/fs/cgroup/cpu,cpuacct/docker/x/cpu.cfs_quota_us":  "100000\n",
		"sys/fs/cgroup/cpu,cpuacct/docker/x/cpu.cfs_period_us": "100000\n",
	})
	if c := DetectAt(linuxProbe(v1)).CPU; c.Cores != 1 {
		t.Fatalf("v1 quota: %+v", c)
	}
}

// T07: the rule table.
func TestTierRules(t *testing.T) {
	nvidia := Machine{GPU: GPUs{NVIDIA: []GPU{{Name: "RTX 3060", MemoryMB: 12288}}}, CPU: CPU{AVX2: true}}
	render := Machine{GPU: GPUs{Render: []RenderDevice{{Path: "/dev/dri/renderD128", Driver: "i915"}}}}
	cases := []struct {
		limit uint64
		arch  string
		m     Machine
		want  Tier
		model string
		hw    int
	}{
		{GiB * 37 / 10, "amd64", Machine{}, TierLight, ModelBase, 2},
		{4 * GiB, "arm64", Machine{}, TierLight, ModelBase, 2},
		{7 * GiB, "arm64", Machine{}, TierLight, ModelBase, 2},
		{8 * GiB, "amd64", Machine{CPU: CPU{AVX2: true}}, TierStandard, ModelSmall, 4},
		{8 * GiB, "amd64", nvidia, TierStandard, ModelTurbo, 4},
		{6 * GiB, "amd64", render, TierStandard, ModelBase, 4},
		{16 * GiB, "amd64", Machine{CPU: CPU{AVX2: true}}, TierStrong, ModelTurbo, 0},
		{0, "amd64", Machine{}, TierStrong, ModelSmall, 0},
	}
	for _, c := range cases {
		c.m.Memory.LimitBytes = c.limit
		got := TierFor(c.limit, c.arch)
		d := DefaultsFor(got, c.m)
		if got != c.want || d.SpeechModel != c.model || d.HardwareTranscodes != c.hw {
			t.Errorf("(%v GiB, %s): %s %s %d, want %s %s %d", GB(c.limit), c.arch, got, d.SpeechModel, d.HardwareTranscodes, c.want, c.model, c.hw)
		}
	}
	light := DefaultsFor(TierLight, Machine{})
	if light.SpeechOn || light.PipelineWorkers != 1 || light.QuietHours != DefaultQuietHours || light.SharedBuffers != "256MB" || !light.SpritesInQuietHours {
		t.Fatalf("light: %+v", light)
	}
	if s := DefaultsFor(TierStrong, Machine{Memory: Memory{LimitBytes: 64 * GiB}}); s.SharedBuffers != "4096MB" || s.QuietHours != "" || s.PipelineWorkers != 0 {
		t.Fatalf("strong 64 GB: %+v", s)
	}
	if s := DefaultsFor(TierStrong, Machine{Memory: Memory{LimitBytes: 16 * GiB}}); s.SharedBuffers != "4096MB" {
		t.Fatalf("strong 16 GB: %+v", s)
	}
	if s := DefaultsFor(TierStrong, Machine{Memory: Memory{LimitBytes: 12 * GiB}}); s.SharedBuffers != "3072MB" {
		t.Fatalf("strong 12 GB (picked by an admin): %+v", s)
	}
	if MinMemory > GiB*37/10 || MinMemory < 3*GiB {
		t.Fatal("the floor lets a 3.7 GiB NAS in and keeps 3 GiB out")
	}
	for _, s := range []string{"light", " Standard ", "STRONG"} {
		if _, ok := ParseTier(s); !ok {
			t.Errorf("ParseTier(%q)", s)
		}
	}
	if _, ok := ParseTier("auto"); ok {
		t.Fatal("auto is not a tier")
	}
}

// The data disk: btrfs on DSM's cachedev through dm and md to rotational disks; ext4 on an
// NVMe partition; ZFS with no device; a FUSE merged share.
func TestDataDisk(t *testing.T) {
	root := fixture(t, map[string]string{
		"proc/self/mountinfo": "20 1 0:30 / / rw - overlay overlay rw\n" +
			"30 20 0:45 /@appdata/maktaba /var/lib/maktaba rw,relatime - btrfs /dev/mapper/cachedev_0 rw\n" +
			"31 20 259:2 / /nvme rw - ext4 /dev/nvme0n1p1 rw\n" +
			"32 20 0:50 / /mnt/tank rw - zfs tank/apps rw\n" +
			"33 20 0:51 / /mnt/user rw - fuse.shfs shfs rw\n" +
			"34 20 0:52 / /mnt/my\\040share rw - ext4 /dev/sdc1 rw\n",
		"sys/class/block/dm-0/dm/name":             "cachedev_0\n",
		"sys/class/block/dm-0/slaves/md2/":         "",
		"sys/class/block/md2/slaves/sda3/":         "",
		"sys/class/block/md2/slaves/sdb3/":         "",
		"sys/class/block/sda3/partition":           "3",
		"sys/class/block/sdb3/partition":           "3",
		"sys/class/block/sda/queue/rotational":     "1\n",
		"sys/class/block/sdb/queue/rotational":     "1\n",
		"sys/class/block/nvme0n1p1/partition":      "1",
		"sys/class/block/nvme0n1/queue/rotational": "0\n",
		"sys/class/block/sdc1/partition":           "1",
		"sys/class/block/sdc/queue/rotational":     "0\n",
	})
	p := linuxProbe(root)
	d := DataDiskAt(p, "/var/lib/maktaba/postgres")
	if d.Kind != DiskRotational || d.FSType != "btrfs" || len(d.Disks) != 2 || d.Disks[0].Name != "sda" {
		t.Fatalf("DSM btrfs: %+v", d)
	}
	if d := DataDiskAt(p, "/nvme/maktaba"); d.Kind != DiskSSD || d.Disks[0].Name != "nvme0n1" {
		t.Fatalf("nvme: %+v", d)
	}
	if d := DataDiskAt(p, "/mnt/tank/maktaba"); d.Kind != DiskUnknown || d.FSType != "zfs" || d.Source != "tank/apps" {
		t.Fatalf("zfs: %+v", d)
	}
	if d := DataDiskAt(p, "/mnt/user/appdata"); d.Kind != DiskUnknown || d.FSType != "fuse.shfs" {
		t.Fatalf("shfs: %+v", d)
	}
	if d := DataDiskAt(p, "/mnt/my share/x"); d.Kind != DiskSSD || d.Mount != "/mnt/my share" {
		t.Fatalf("escaped mount: %+v", d)
	}
	if d := DataDiskAt(p, "/etc"); d.FSType != "overlay" || d.Kind != DiskUnknown {
		t.Fatalf("root overlay: %+v", d)
	}
}

// This machine reads without failing, and its tier is one of the three.
func TestDetectThisMachine(t *testing.T) {
	m := Detect()
	if m.Arch == "" || m.Platform == "" || m.CPU.Cores < 1 {
		t.Fatalf("this machine: %+v", m)
	}
	if TierFor(m.Memory.LimitBytes, m.Arch).Rank() == 0 {
		t.Fatal("no tier")
	}
	if MemoryLimit() == 0 || CPUCores() < 1 {
		t.Fatalf("limit %d, cores %d", MemoryLimit(), CPUCores())
	}
	_ = DataDiskOf(t.TempDir())
}
