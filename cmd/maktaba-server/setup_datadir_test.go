package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
)

func fakeDataDisk(t *testing.T, disk storage.Disk, dd hostinfo.DataDisk, err error) {
	t.Helper()
	prev := dataDiskFacts
	t.Cleanup(func() { dataDiskFacts = prev })
	dataDiskFacts = func(string) (storage.Disk, hostinfo.DataDisk, error) { return disk, dd, err }
}

func noteCodes(notes []checkNote) map[string]checkNote {
	out := map[string]checkNote{}
	for _, n := range notes {
		out[n.Code] = n
	}
	return out
}

// Epic 59 story 59.02 T08: a data folder on Unraid's merged share is refused, naming the
// file system and giving the cache-pool advice; a network share names its file system; a
// spinning disk warns; a ZFS dataset whose disk cannot be read asks; an SSD says nothing.
func TestCheckDataDir(t *testing.T) {
	fakeDataDisk(t, storage.Disk{FSType: "fuse.shfs", Mount: "/mnt/user"}, hostinfo.DataDisk{Kind: hostinfo.DiskUnknown}, nil)
	res := checkResult{OK: true}
	checkDataDir("/mnt/user/appdata/maktaba", &res)
	p := noteCodes(res.Problems)["data_fuse"]
	if res.OK || p.Params["fstype"] != "fuse.shfs" || !strings.Contains(p.Message, "/mnt/cache/appdata/maktaba") {
		t.Fatalf("shfs: %+v", res)
	}

	fakeDataDisk(t, storage.Disk{FSType: "nfs4", Mount: "/data"}, hostinfo.DataDisk{}, nil)
	res = checkResult{OK: true}
	checkDataDir("/data/maktaba", &res)
	if p := noteCodes(res.Problems)["data_fs_refused"]; res.OK || p.Params["fstype"] != "nfs4" || !strings.Contains(p.Message, "nfs4") {
		t.Fatalf("nfs: %+v", res)
	}

	fakeDataDisk(t, storage.Disk{FSType: "btrfs", Suitable: true, Local: true}, hostinfo.DataDisk{Kind: hostinfo.DiskRotational, FSType: "btrfs"}, nil)
	res = checkResult{OK: true}
	checkDataDir("/volume1/docker/maktaba", &res)
	if !res.OK || noteCodes(res.Warnings)["data_rotational"].Code == "" || res.DataDisk == nil {
		t.Fatalf("rotational: %+v", res)
	}

	fakeDataDisk(t, storage.Disk{FSType: "zfs", Suitable: true, Local: true}, hostinfo.DataDisk{Kind: hostinfo.DiskUnknown, FSType: "zfs"}, nil)
	res = checkResult{OK: true}
	checkDataDir("/mnt/tank/apps/maktaba", &res)
	if !res.OK || noteCodes(res.Warnings)["data_ask_ssd"].Code == "" {
		t.Fatalf("zfs: %+v", res)
	}

	fakeDataDisk(t, storage.Disk{FSType: "ext4", Suitable: true, Local: true}, hostinfo.DataDisk{Kind: hostinfo.DiskSSD, FSType: "ext4"}, nil)
	res = checkResult{OK: true}
	checkDataDir("/srv/maktaba", &res)
	if !res.OK || len(res.Warnings) != 0 {
		t.Fatalf("ssd: %+v", res)
	}

	fakeDataDisk(t, storage.Disk{}, hostinfo.DataDisk{}, errors.New("gone"))
	res = checkResult{OK: true}
	checkDataDir("/x", &res)
	if !res.OK || len(res.Problems) != 0 {
		t.Fatalf("unreadable: %+v", res)
	}
}
