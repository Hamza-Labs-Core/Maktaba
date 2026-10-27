//go:build linux

package storage

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// within is config.Within.
func within(p, root string) bool { return config.Within(p, root) }

// mountEntry is one line of /proc/self/mounts.
type mountEntry struct{ device, mount, fsType string }

func readMounts() []mountEntry {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []mountEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		out = append(out, mountEntry{device: fields[0], mount: unescapeMount(fields[1]), fsType: fields[2]})
	}
	return out
}

// unescapeMount undoes /proc/mounts' octal escapes (\040 for a space).
func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

// localFS are the file systems of this machine's own disks.
var localFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true,
	"f2fs": true, "bcachefs": true, "jfs": true, "reiserfs": true, "vfat": true, "exfat": true, "ntfs": true,
	"ntfs3": true, "fuseblk": true, "hfsplus": true, "apfs": true}

func platformStatVolume(dir string) (volume, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return volume{}, err
	}
	// the mount that holds dir: the longest mount point it is under
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	best := mountEntry{mount: "/"}
	for _, m := range readMounts() {
		if within(resolved, m.mount) && len(m.mount) >= len(best.mount) {
			best = m
		}
	}
	return volume{mount: best.mount, name: filepath.Base(best.mount), fsType: best.fsType,
		free: st.Bavail * uint64(st.Bsize), total: st.Blocks * uint64(st.Bsize), local: localFS[best.fsType]}, nil
}

// platformListVolumes: the root file system and the disks mounted under /mnt,
// /media, /run/media, /srv and /home (the places a disk is mounted for use).
func platformListVolumes() ([]volume, error) {
	var out []volume
	for _, m := range readMounts() {
		if !localFS[m.fsType] {
			continue
		}
		ok := m.mount == "/"
		for _, p := range []string{"/mnt", "/media", "/run/media", "/srv", "/home", "/data"} {
			ok = ok || within(m.mount, p)
		}
		if !ok {
			continue
		}
		v, err := platformStatVolume(m.mount)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func isSystemMount(mount string) bool { return mount == "/" }
