// Package storage is where an install keeps its large state and its move to
// another disk (Epic 36 story 36.15): the disks this machine has with their free
// space, what each of the install's folders holds, and the move itself (a copy
// that is verified, then server.toml pointed at it, the old copy kept aside).
package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Disk is one mounted volume.
type Disk struct {
	Mount      string `json:"mount"`
	Name       string `json:"name"`
	FreeBytes  uint64 `json:"free_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
	FSType     string `json:"fs_type"`
	// Local: a disk of this machine (not a network share).
	Local bool `json:"local"`
	// Suitable: a local file system the database can live on (permissions, fsync,
	// links); Problem says why not.
	Suitable bool   `json:"suitable"`
	Problem  string `json:"problem,omitempty"`
	// SuggestedDir is the folder a move or setup proposes on it.
	SuggestedDir string `json:"suggested_dir"`
	// Internal: the disk the system runs from.
	Internal bool `json:"internal"`
	// Current: the disk the install's data is on now.
	Current bool `json:"current"`
}

// volume is what the platform reports about the volume that holds a folder.
type volume struct {
	mount, name, fsType string
	free, total         uint64
	local               bool
}

// statVolume is the platform's (tests replace it).
var statVolume = platformStatVolume

// listVolumes is the platform's mounted volumes (tests replace it).
var listVolumes = platformListVolumes

// DiskOf is the disk that holds dir, or would (its nearest folder that exists).
func DiskOf(dir string) (Disk, error) {
	p := filepath.Clean(dir)
	for {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			break
		}
		up := filepath.Dir(p)
		if up == p {
			break
		}
		p = up
	}
	v, err := statVolume(p)
	if err != nil {
		return Disk{}, err
	}
	return diskOf(v), nil
}

func diskOf(v volume) Disk {
	d := Disk{Mount: v.mount, Name: v.name, FreeBytes: v.free, TotalBytes: v.total, FSType: v.fsType, Local: v.local,
		Internal: isSystemMount(v.mount)}
	if d.Name == "" {
		d.Name = filepath.Base(v.mount)
	}
	d.Suitable, d.Problem = suitable(v)
	return d
}

// suitable: a local volume whose file system the database can live on.
func suitable(v volume) (bool, string) {
	if !v.local {
		return false, "target_not_local"
	}
	fs := strings.ToLower(v.fsType)
	if unsuitableFS[fs] || (runtime.GOOS == "darwin" && fs == "ntfs") {
		return false, "target_not_local"
	}
	return true, ""
}

// unsuitableFS are file systems Postgres must not live on: no owners and modes
// (FAT, exFAT), read-only or unreliable drivers (NTFS on a Mac), network and
// memory file systems.
var unsuitableFS = map[string]bool{
	"msdos": true, "vfat": true, "fat": true, "fat32": true, "exfat": true,
	"smbfs": true, "nfs": true, "nfs4": true, "afpfs": true, "webdav": true, "cifs": true, "smb3": true,
	"fuse.sshfs": true, "sshfs": true, "9p": true, "tmpfs": true, "ramfs": true, "autofs": true, "devfs": true,
	"iso9660": true, "udf": true, "cd9660": true, "squashfs": true,
}

// ListDisks is every mounted volume a move or setup may choose, the internal one
// first, then by free space; current is the install's data folder now ("" for
// none), whose disk is marked. Each one's suggested folder is
// <mount>/Maktaba (the default data folder on the system disk).
func ListDisks(current, defaultData string) []Disk {
	vols, _ := listVolumes()
	var cur Disk
	if current != "" {
		cur, _ = DiskOf(current)
	}
	seen := map[string]bool{}
	var out []Disk
	for _, v := range vols {
		if seen[v.mount] {
			continue
		}
		seen[v.mount] = true
		d := diskOf(v)
		d.SuggestedDir = suggestedDir(d, defaultData)
		d.Current = cur.Mount != "" && cur.Mount == d.Mount
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Internal != out[j].Internal {
			return out[i].Internal
		}
		return out[i].FreeBytes > out[j].FreeBytes
	})
	return out
}

// suggestedDir is the folder proposed on d: the default data folder on the disk
// that holds it, else <mount>/Maktaba.
func suggestedDir(d Disk, defaultData string) string {
	if defaultData != "" {
		if dd, err := DiskOf(defaultData); err == nil && dd.Mount == d.Mount {
			return defaultData
		}
	}
	return filepath.Join(d.Mount, "Maktaba")
}

// Suggest is the folder on the suitable disk with the most free room (the
// default data folder when no other disk has more).
func Suggest(disks []Disk, defaultData string) string {
	best := -1
	for i, d := range disks {
		if !d.Suitable {
			continue
		}
		if best < 0 || d.FreeBytes > disks[best].FreeBytes {
			best = i
		}
	}
	if best < 0 {
		return defaultData
	}
	return disks[best].SuggestedDir
}
