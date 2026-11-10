package pgbundle

// Postgres tuned to the machine (Epic 59 story 59.02): its memory by the hardware tier
// (hostinfo.DefaultsFor), longer checkpoints and fewer random reads on spinning disks, and
// the household's [postgres] settings last, which win. max_connections stays
// MaxConnections on every tier: lowering it would need every pool sized per tier first.

import (
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// RotationalSettings are what spinning disks get.
var RotationalSettings = []string{"random_page_cost=4", "effective_io_concurrency=2",
	"checkpoint_timeout=15min", "wal_compression=on"}

// dataDiskOf reads the disk under the cluster (tests replace it).
var dataDiskOf = hostinfo.DataDiskOf

// memoryLimit is the memory the machine may use (tests replace it).
var memoryLimit = hostinfo.MemoryLimit

// DataRotational says whether the database's disk spins: the household's answer
// ([server] data_disk) when there is one, else what /sys says; a disk that cannot be read
// and was not answered counts as spinning on Linux (the safe tuning on a NAS). Second: how it was known
// ("rotational", "ssd", "asked" or "unknown").
func DataRotational(cfg config.Config, dir string) (bool, string) {
	switch strings.ToLower(strings.TrimSpace(cfg.Server.DataDisk)) {
	case "ssd":
		return false, "asked"
	case "rotational":
		return true, "asked"
	}
	switch d := dataDiskOf(dir); d.Kind {
	case hostinfo.DiskSSD:
		return false, d.Kind
	case hostinfo.DiskRotational:
		return true, d.Kind
	}
	// off Linux nothing is read (a Mac's and a PC's disks are SSDs nearly always)
	return goos == "linux", hostinfo.DiskUnknown
}

// goos is runtime.GOOS (tests replace it).
var goos = runtime.GOOS

// settingName is a Postgres setting's name (a class.name for an extension's).
var settingName = regexp.MustCompile(`^[a-z_][a-z0-9_]*(\.[a-z_][a-z0-9_]*)?$`)

// Tuning are the -c settings for cfg's machine, its cluster at dir: none without a tier
// (a single role, an older start), the household's alone still.
func Tuning(cfg config.Config, dir string) []string {
	var out []string
	if tier, ok := hostinfo.ParseTier(cfg.Server.Tier); ok {
		d := hostinfo.DefaultsFor(tier, hostinfo.Machine{Memory: hostinfo.Memory{LimitBytes: memoryLimit()}})
		out = append(out, "shared_buffers="+d.SharedBuffers, "work_mem="+d.WorkMem,
			"maintenance_work_mem="+d.MaintenanceWorkMem)
		if rot, _ := DataRotational(cfg, dir); rot {
			out = append(out, RotationalSettings...)
		}
	}
	keys := make([]string, 0, len(cfg.Postgres.Settings))
	for k := range cfg.Postgres.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		name := strings.ToLower(strings.TrimSpace(k))
		v := strings.TrimSpace(cfg.Postgres.Settings[k])
		if !settingName.MatchString(name) || v == "" || strings.ContainsAny(v, "\n\r\x00") {
			continue
		}
		out = append(out, name+"="+v)
	}
	return out
}
