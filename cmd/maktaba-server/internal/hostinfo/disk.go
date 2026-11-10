package hostinfo

// The disk under a folder (Epic 59 stories 59.04 and 59.02): whether Maktaba's data folder
// is on spinning disks. The mount is found in /proc/self/mountinfo, not with stat: btrfs
// (most Synology volumes) and ZFS give an anonymous device number (major 0) that leads to
// no disk. The mount's source (/dev/mapper/cachedev_0, /dev/md2, /dev/sdb1) is followed
// through /sys/class/block/<dev>/slaves/ across dm and md layers to the disks, and their
// queue/rotational is read: any rotational member makes the folder rotational.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Disk kinds (DataDisk.Kind).
const (
	DiskRotational = "rotational"
	DiskSSD        = "ssd"
	// DiskUnknown: no device could be followed (a ZFS dataset in a container, an
	// overlay with no backing device): setup asks (59.02).
	DiskUnknown = "unknown"
)

// DataDisk is the disk under a folder.
type DataDisk struct {
	Kind string `json:"kind"`
	// Mount is the mount point that holds the folder, FSType its file system (ext4,
	// btrfs, zfs, fuse.shfs, nfs4, …) and Source what is mounted (/dev/md2, tank/apps).
	Mount  string `json:"mount,omitempty"`
	FSType string `json:"fs_type,omitempty"`
	Source string `json:"source,omitempty"`
	// Disks are the block devices found under it (sda, nvme0n1), each with whether it
	// spins.
	Disks []BlockDisk `json:"disks,omitempty"`
}

// BlockDisk is one disk under a mount.
type BlockDisk struct {
	Name       string `json:"name"`
	Rotational bool   `json:"rotational"`
}

// MountEntry is one line of /proc/self/mountinfo.
type MountEntry struct {
	Mount  string
	FSType string
	Source string
}

// DataDiskOf is the disk under dir on this machine (Kind unknown off Linux).
func DataDiskOf(dir string) DataDisk {
	if runtime.GOOS != "linux" {
		return DataDisk{Kind: DiskUnknown}
	}
	return DataDiskAt(Probe{Root: "/"}, dir)
}

// MountOf is the mount that holds dir on this machine (ok false off Linux or when
// mountinfo cannot be read).
func MountOf(dir string) (MountEntry, bool) {
	if runtime.GOOS != "linux" {
		return MountEntry{}, false
	}
	return mountAt(Probe{Root: "/"}, realDir(dir))
}

// realDir is dir with its symlinks followed, or the nearest folder that exists.
func realDir(dir string) string {
	for p := filepath.Clean(dir); ; {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			if rest, _ := filepath.Rel(p, filepath.Clean(dir)); rest != "." && rest != "" {
				return filepath.Join(r, rest)
			}
			return r
		}
		up := filepath.Dir(p)
		if up == p {
			return filepath.Clean(dir)
		}
		p = up
	}
}

// DataDiskAt is DataDiskOf against p's root; dir is a path inside that machine.
func DataDiskAt(p Probe, dir string) DataDisk {
	if p.Root == "" {
		p.Root = "/"
	}
	if p.Root == "/" {
		dir = realDir(dir)
	}
	d := DataDisk{Kind: DiskUnknown}
	mt, ok := mountAt(p, dir)
	if !ok {
		return d
	}
	d.Mount, d.FSType, d.Source = mt.Mount, mt.FSType, mt.Source
	if !strings.HasPrefix(mt.Source, "/dev/") {
		return d // zfs (tank/apps), overlay, tmpfs, a network share: no device to follow
	}
	name := blockName(p, mt.Source)
	if name == "" {
		return d
	}
	seen := map[string]bool{}
	var walk func(n string, depth int)
	walk = func(n string, depth int) {
		if seen[n] || depth > 8 {
			return
		}
		seen[n] = true
		slaves, _ := os.ReadDir(p.at("/sys/class/block/" + n + "/slaves"))
		if len(slaves) > 0 {
			for _, s := range slaves {
				walk(s.Name(), depth+1)
			}
			return
		}
		if r, ok := rotationalOf(p, n); ok {
			d.Disks = append(d.Disks, BlockDisk{Name: diskName(p, n), Rotational: r})
		}
	}
	walk(name, 0)
	if len(d.Disks) == 0 {
		return d
	}
	d.Kind = DiskSSD
	for _, b := range d.Disks {
		if b.Rotational {
			d.Kind = DiskRotational
		}
	}
	return d
}

// readMountinfo is /proc/self/mountinfo's entries (octal escapes decoded).
func readMountinfo(p Probe) []MountEntry {
	s, _ := p.read("/proc/self/mountinfo")
	var out []MountEntry
	for _, line := range strings.Split(s, "\n") {
		// id parent major:minor root mount options [optional…] - fstype source superopts
		pre, post, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		f, g := strings.Fields(pre), strings.Fields(post)
		if len(f) < 5 || len(g) < 2 {
			continue
		}
		out = append(out, MountEntry{Mount: unescape(f[4]), FSType: g[0], Source: unescape(g[1])})
	}
	return out
}

// mountAt is the mount whose point is the longest prefix of dir (the last one listed
// wins a tie: it is mounted over the others).
func mountAt(p Probe, dir string) (MountEntry, bool) {
	dir = filepath.Clean("/" + dir)
	var best MountEntry
	found := false
	for _, mt := range readMountinfo(p) {
		m := filepath.Clean(mt.Mount)
		if dir == m || m == "/" || strings.HasPrefix(dir, m+"/") {
			if !found || len(m) >= len(filepath.Clean(best.Mount)) {
				best, found = mt, true
			}
		}
	}
	return best, found
}

// unescape decodes mountinfo's \040-style escapes.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if c, ok := octal(s[i+1 : i+4]); ok {
				b.WriteByte(c)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func octal(s string) (byte, bool) {
	var n int
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, false
		}
		n = n*8 + int(c-'0')
	}
	return byte(n), n < 256
}

// blockName is the /sys/class/block name of a /dev source: /dev/mapper/<name> is the dm
// device whose dm/name is <name>; /dev/sdb1 is sdb1.
func blockName(p Probe, source string) string {
	if strings.HasPrefix(source, "/dev/mapper/") {
		want := strings.TrimPrefix(source, "/dev/mapper/")
		entries, _ := os.ReadDir(p.at("/sys/class/block"))
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "dm-") {
				continue
			}
			if v, ok := p.read("/sys/class/block/" + e.Name() + "/dm/name"); ok && v == want {
				return e.Name()
			}
		}
		// a container that has /dev/mapper: the link names the dm device
		if t, err := os.Readlink(p.at(source)); err == nil {
			return filepath.Base(t)
		}
		return ""
	}
	n := filepath.Base(source)
	if p.exists("/sys/class/block/" + n) {
		return n
	}
	return ""
}

// rotationalOf reads the disk's queue/rotational; a partition (sdb1, nvme0n1p1) has none
// of its own, so its disk's is read.
func rotationalOf(p Probe, n string) (bool, bool) {
	disk := diskName(p, n)
	v, ok := p.read("/sys/class/block/" + disk + "/queue/rotational")
	if !ok {
		return false, false
	}
	return v == "1", true
}

// diskName is the disk a partition is on: the folder its /sys entry resolves into
// (…/sdb/sdb1), else the name without its partition number.
func diskName(p Probe, n string) string {
	if p.exists("/sys/class/block/" + n + "/queue") {
		return n
	}
	if t, err := filepath.EvalSymlinks(p.at("/sys/class/block/" + n)); err == nil {
		if parent := filepath.Base(filepath.Dir(t)); p.exists("/sys/class/block/" + parent + "/queue") {
			return parent
		}
	}
	trimmed := strings.TrimRight(n, "0123456789")
	if strings.HasSuffix(trimmed, "p") && (strings.HasPrefix(n, "nvme") || strings.HasPrefix(n, "mmcblk")) {
		trimmed = strings.TrimSuffix(trimmed, "p")
	}
	if trimmed != "" && trimmed != n && p.exists("/sys/class/block/"+trimmed+"/queue") {
		return trimmed
	}
	return n
}
