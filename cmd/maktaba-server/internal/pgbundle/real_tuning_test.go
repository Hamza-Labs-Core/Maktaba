//go:build integration && !windows

package pgbundle

import (
	"context"
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// Epic 59 story 59.02 T04 and T05: a Light home on spinning disks starts Postgres with the
// tier's memory and the disk's values, max_connections stays 300, and a household's
// [postgres] settings win.
func TestRealPostgresTuning(t *testing.T) {
	home := realHome(t)
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	prevD, prevG := dataDiskOf, goos
	t.Cleanup(func() { dataDiskOf, goos = prevD, prevG })
	dataDiskOf = func(string) hostinfo.DataDisk { return hostinfo.DataDisk{Kind: hostinfo.DiskRotational} }
	goos = "linux"
	root, err := os.MkdirTemp("", "mkpgt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	cfg := config.Defaults()
	cfg.Server.Tier = "light"
	cfg.Postgres.Settings = map[string]string{"work_mem": "6MB"}
	b := Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "backups"), Port: testport.Free(t), Tuning: Tuning(cfg, filepath.Join(root, "pg"))}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command(b.Bin("postgres"), b.ServerArgs()...)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Signal(os.Interrupt); _ = srv.Wait() })
	deadline := time.Now().Add(30 * time.Second)
	for Ready(ctx, b.Addr(), AppRole, AppDatabase) != nil {
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	url, err := b.URL()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for name, want := range map[string]string{"shared_buffers": "256MB", "work_mem": "6MB",
		"maintenance_work_mem": "64MB", "max_connections": "300", "random_page_cost": "4",
		"effective_io_concurrency": "2", "checkpoint_timeout": "15min", "wal_compression": "on"} {
		var got string
		if err := db.QueryRowContext(ctx, "SHOW "+name).Scan(&got); err != nil {
			t.Fatalf("SHOW %s: %v", name, err)
		}
		if got != want && !(name == "wal_compression" && got == "pglz") {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
