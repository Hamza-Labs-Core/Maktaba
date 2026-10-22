package pgbundle

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Until a machine may connect (one approved, or joins open), the server listens on this
// machine only and pg_hba.conf lets no machine in; then it listens beyond it, and lets
// the machines in from their networks and, by default, each one's own address.
func TestTheMachinesReachOnlyWhatTheClusterAsks(t *testing.T) {
	dir := t.TempDir()
	b := Bundle{Data: filepath.Join(dir, "postgres"), Keys: filepath.Join(dir, "keys"), Port: DefaultPort, LAN: true, TLS: true}
	if err := os.MkdirAll(b.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	listen := func() string {
		for _, a := range b.ServerArgs() {
			if v, ok := strings.CutPrefix(a, "listen_addresses="); ok {
				return v
			}
		}
		return ""
	}
	if got := listen(); got != "127.0.0.1" {
		t.Errorf("no machine yet: listens on %q", got)
	}
	if hba := b.hbaConf(); strings.Contains(hba, "maktaba_worker") {
		t.Errorf("no machine yet, but pg_hba.conf lets them in:\n%s", hba)
	}

	r := Reach{Open: true, Addrs: []string{"100.64.1.2", "192.168.1.7", "127.0.0.1", "bogus", "fd7a::5", "100.64.1.2"}}
	if err := b.KeepReach(r); err != nil {
		t.Fatal(err)
	}
	if got := b.LastReach(); !got.Open || len(got.Addrs) != len(r.Addrs) {
		t.Fatalf("kept %+v, read back %+v", r, got)
	}
	if got := listen(); got != "*" {
		t.Errorf("a machine may connect: listens on %q", got)
	}
	hba := b.hbaConf()
	for _, want := range []string{
		"hostssl maktaba      +maktaba_worker  samenet",
		"hostssl maktaba      +maktaba_replicas samenet",
		"hostssl maktaba      +maktaba_worker  100.64.1.2/32",
		"hostssl maktaba      +maktaba_replicas 192.168.1.7/32",
		"hostssl maktaba      +maktaba_worker  fd7a::5/128",
	} {
		if !strings.Contains(hba, want) {
			t.Errorf("pg_hba.conf lacks %q:\n%s", want, hba)
		}
	}
	if strings.Count(hba, "100.64.1.2/32") != 2 || strings.Contains(hba, "bogus") ||
		strings.Contains(hba, "maktaba_worker  127.0.0.1") {
		t.Errorf("one line per group and machine address, never this machine's own:\n%s", hba)
	}

	// networks the admin named: those only
	b.MachinesFrom = "10.8.0.0/16"
	if hba := b.hbaConf(); strings.Contains(hba, "100.64.1.2") || !strings.Contains(hba, "10.8.0.0/16") {
		t.Errorf("machines_from named networks, yet:\n%s", hba)
	}
	b.MachinesFrom = ""

	// [postgres] listen = "loopback": never beyond this machine
	b.LAN = false
	if got := listen(); got != "127.0.0.1" {
		t.Errorf("listen = loopback: listens on %q", got)
	}
	if hba := b.hbaConf(); strings.Contains(hba, "maktaba_worker") {
		t.Errorf("listen = loopback, but pg_hba.conf lets machines in:\n%s", hba)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b.WatchReach(ctx, time.Millisecond, func() error { t.Error("listen = loopback, yet started again"); return nil }, io.Discard)
}
