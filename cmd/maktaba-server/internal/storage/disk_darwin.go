//go:build darwin

package storage

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func cstr(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func fromStatfs(st *unix.Statfs_t) volume {
	mount := cstr(st.Mntonname[:])
	fs := cstr(st.Fstypename[:])
	v := volume{mount: mount, fsType: fs, free: st.Bavail * uint64(st.Bsize), total: st.Blocks * uint64(st.Bsize),
		local: st.Flags&unix.MNT_LOCAL != 0}
	v.name = filepath.Base(mount)
	if mount == "/" || mount == "/System/Volumes/Data" {
		v.name = "Macintosh HD"
		v.mount = "/"
	}
	return v
}

func platformStatVolume(dir string) (volume, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return volume{}, err
	}
	return fromStatfs(&st), nil
}

// platformListVolumes: the system disk and every volume under /Volumes the user
// can write to (no system volumes: Preboot, VM, Recovery, the firmlinked ones).
func platformListVolumes() ([]volume, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	var out []volume
	for i := range buf[:n] {
		v := fromStatfs(&buf[i])
		mount := cstr(buf[i].Mntonname[:])
		switch {
		case mount == "/System/Volumes/Data":
		case strings.HasPrefix(mount, "/Volumes/") && buf[i].Flags&unix.MNT_RDONLY == 0:
		default:
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func isSystemMount(mount string) bool { return mount == "/" }
