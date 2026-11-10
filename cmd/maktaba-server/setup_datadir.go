package main

// The data folder on a NAS (Epic 59 story 59.02): it holds the bundled Postgres, so it must
// be on a local disk, and should be on an SSD. check refuses a folder on a network share,
// FAT/exFAT or a FUSE file system, naming the file system it found; a FUSE merged share
// (Unraid's /mnt/user, mergerfs) gets the cache-pool advice. On spinning disks it warns
// (Postgres is tuned for them, pgbundle/tuning.go); when the disk cannot be read (a ZFS
// dataset in a container) it asks, and Settings → This machine takes the answer.

import (
	"fmt"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
)

// dataDiskFacts read the data folder's disk: its file system and suitability
// (storage.DiskOf), and whether it spins (hostinfo.DataDiskOf). Tests replace it.
var dataDiskFacts = func(dir string) (storage.Disk, hostinfo.DataDisk, error) {
	d, err := storage.DiskOf(dir)
	return d, hostinfo.DataDiskOf(dir), err
}

// mergedShare: a FUSE file system that pools disks (Unraid's shfs, mergerfs).
func mergedShare(fs string) bool {
	fs = strings.ToLower(fs)
	return fs == "fuse.shfs" || fs == "shfs" || strings.Contains(fs, "mergerfs")
}

func checkDataDir(dir string, res *checkResult) {
	disk, dd, err := dataDiskFacts(dir)
	if err != nil {
		return // the disk could not be read: the storage step says so
	}
	fs := disk.FSType
	if fs == "" {
		fs = dd.FSType
	}
	switch {
	case strings.EqualFold(fs, "overlay"):
		return // a container's own layer with no volume mounted: local, nothing to read under it
	case mergedShare(fs):
		res.Problem("data_fuse", fmt.Sprintf("Your data folder is on a merged share (%s). Put it on a cache pool or a single "+
			"disk (for example /mnt/cache/appdata/maktaba).", dir), "path", dir, "fstype", fs)
		return
	case !disk.Suitable || strings.HasPrefix(strings.ToLower(fs), "fuse"):
		res.Problem("data_fs_refused", fmt.Sprintf("Maktaba can't keep its database on %s (%s). Choose a folder on a local disk.",
			fsOrUnknown(fs), dir), "fstype", fsOrUnknown(fs), "path", dir)
		return
	}
	switch dd.Kind {
	case hostinfo.DiskRotational:
		res.Warn("data_rotational", "The database is on spinning disks. Maktaba will work, but browsing and search are "+
			"faster on an SSD, and the disks will not sleep while Maktaba runs.")
	case hostinfo.DiskUnknown:
		if dd.FSType == "zfs" {
			res.Warn("data_ask_ssd", "Is Maktaba's data folder on an SSD? Maktaba could not tell: until you answer in "+
				"Settings → This machine, the database is tuned for spinning disks.")
		}
	}
	res.DataDisk = &dd
}

func fsOrUnknown(fs string) string {
	if fs == "" {
		return "an unknown file system"
	}
	return fs
}
