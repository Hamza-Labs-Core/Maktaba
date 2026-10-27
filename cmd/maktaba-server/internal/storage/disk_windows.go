//go:build windows

package storage

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func platformStatVolume(dir string) (volume, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return volume{}, err
	}
	root := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &root[0], uint32(len(root))); err != nil {
		return volume{}, err
	}
	mount := windows.UTF16ToString(root)
	return statRoot(mount)
}

func statRoot(mount string) (volume, error) {
	rp, err := windows.UTF16PtrFromString(mount)
	if err != nil {
		return volume{}, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(rp, &free, &total, &totalFree); err != nil {
		return volume{}, err
	}
	name := make([]uint16, windows.MAX_PATH+1)
	fs := make([]uint16, windows.MAX_PATH+1)
	_ = windows.GetVolumeInformation(rp, &name[0], uint32(len(name)), nil, nil, nil, &fs[0], uint32(len(fs)))
	kind := windows.GetDriveType(rp)
	v := volume{mount: mount, name: windows.UTF16ToString(name), fsType: windows.UTF16ToString(fs), free: free,
		total: total, local: kind == windows.DRIVE_FIXED || kind == windows.DRIVE_REMOVABLE}
	if v.name == "" {
		v.name = strings.TrimRight(mount, `\`)
	}
	return v, nil
}

// platformListVolumes: the fixed and removable drives with a letter.
func platformListVolumes() ([]volume, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, err
	}
	var out []volume
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		mount := string(rune('A'+i)) + `:\`
		v, err := statRoot(mount)
		if err != nil || !v.local {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func isSystemMount(mount string) bool {
	sys := os.Getenv("SystemDrive")
	if sys == "" {
		sys = "C:"
	}
	return strings.EqualFold(strings.TrimRight(mount, `\`), strings.TrimRight(sys, `\`))
}
