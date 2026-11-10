package pgbundle

import (
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

func fakeTuneDisk(t *testing.T, kind string, os string) {
	t.Helper()
	prevD, prevM, prevG := dataDiskOf, memoryLimit, goos
	t.Cleanup(func() { dataDiskOf, memoryLimit, goos = prevD, prevM, prevG })
	dataDiskOf = func(string) hostinfo.DataDisk { return hostinfo.DataDisk{Kind: kind} }
	memoryLimit = func() uint64 { return 32 << 30 }
	goos = os
}

func setting(args []string, name string) string {
	v := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" && strings.HasPrefix(args[i+1], name+"=") {
			v = strings.TrimPrefix(args[i+1], name+"=") // the last one wins, as in postgres
		}
	}
	return v
}

// T04 (arguments): the Light tier's memory, max_connections 300, on an SSD no disk values.
func TestTuningByTier(t *testing.T) {
	fakeTuneDisk(t, hostinfo.DiskSSD, "linux")
	cfg := config.Defaults()
	cfg.Server.Tier = "light"
	b := Bundle{Data: t.TempDir(), Port: 54329, Tuning: Tuning(cfg, "/d")}
	args := b.serverArgs(false)
	for name, want := range map[string]string{"shared_buffers": "256MB", "work_mem": "4MB",
		"maintenance_work_mem": "64MB", "max_connections": "300", "random_page_cost": ""} {
		if got := setting(args, name); got != want {
			t.Errorf("light %s = %q, want %q", name, got, want)
		}
	}
	cfg.Server.Tier = "standard"
	if got := Tuning(cfg, "/d"); !slices.Contains(got, "shared_buffers=512MB") || !slices.Contains(got, "work_mem=8MB") {
		t.Errorf("standard: %v", got)
	}
	cfg.Server.Tier = "strong"
	if got := Tuning(cfg, "/d"); !slices.Contains(got, "shared_buffers=4096MB") || !slices.Contains(got, "maintenance_work_mem=256MB") {
		t.Errorf("strong on 32 GB: %v", got)
	}
	cfg.Server.Tier = ""
	if got := Tuning(cfg, "/d"); len(got) != 0 {
		t.Errorf("no tier: %v", got)
	}
}

// Spinning disks get the disk values; an answered SSD does not; an unread disk counts as
// spinning on Linux only.
func TestTuningByDisk(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Tier = "standard"
	fakeTuneDisk(t, hostinfo.DiskRotational, "linux")
	if got := Tuning(cfg, "/d"); !slices.Contains(got, "random_page_cost=4") || !slices.Contains(got, "checkpoint_timeout=15min") ||
		!slices.Contains(got, "effective_io_concurrency=2") || !slices.Contains(got, "wal_compression=on") {
		t.Errorf("rotational: %v", got)
	}
	fakeTuneDisk(t, hostinfo.DiskUnknown, "linux")
	if rot, how := DataRotational(cfg, "/d"); !rot || how != "unknown" {
		t.Errorf("T03: not read, not answered: %v %s", rot, how)
	}
	cfg.Server.DataDisk = "ssd"
	if rot, how := DataRotational(cfg, "/d"); rot || how != "asked" {
		t.Errorf("answered ssd: %v %s", rot, how)
	}
	cfg.Server.DataDisk = ""
	fakeTuneDisk(t, hostinfo.DiskUnknown, "darwin")
	if rot, _ := DataRotational(cfg, "/d"); rot {
		t.Error("a Mac's unread disk tuned as spinning")
	}
}

// T05: the household's [postgres] settings win, and a name that is not a setting is left out.
func TestTuningHouseholdWins(t *testing.T) {
	fakeTuneDisk(t, hostinfo.DiskSSD, "linux")
	cfg := config.Defaults()
	cfg.Server.Tier = "light"
	cfg.Postgres.Settings = map[string]string{"shared_buffers": "1GB", "bad name; x": "1", "jit": "off"}
	b := Bundle{Data: t.TempDir(), Port: 54329, Tuning: Tuning(cfg, "/d")}
	args := b.serverArgs(false)
	if got := setting(args, "shared_buffers"); got != "1GB" {
		t.Errorf("shared_buffers = %q, want the household's 1GB", got)
	}
	if got := setting(args, "jit"); got != "off" {
		t.Errorf("jit = %q", got)
	}
	if strings.Contains(strings.Join(args, " "), "bad name") {
		t.Errorf("a bad name passed: %v", args)
	}
}
