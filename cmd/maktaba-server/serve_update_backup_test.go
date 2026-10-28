package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbsafe"
)

// The dump a release's start took before its migrations is found whichever way the start
// named the versions (with or without their v), a file of before or a directory-format
// dump's folder: the run records it with its size.
func TestRehearserFindsTheUpdateDump(t *testing.T) {
	dir := t.TempDir()
	r := rehearser{t: dbsafe.Target{Backups: dir}}
	if r.UpdateDump("1.4.0", "1.5.0") != nil {
		t.Fatal("a dump where there is none")
	}
	p := filepath.Join(dir, "pre-v1.4.0-to-v1.5.0.dump")
	if err := os.WriteFile(p, []byte("dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := r.UpdateDump("1.4.0", "1.5.0"); b == nil || b.Path != p || b.Bytes != 4 {
		t.Fatalf("the dump: %+v", b)
	}
	d := filepath.Join(dir, "pre-1.5.0-to-1.6.0.dump")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"toc.dat": "toc", "3456.dat.gz": "rows!"} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if b := r.UpdateDump("v1.5.0", "v1.6.0"); b == nil || b.Path != d || b.Bytes != 8 {
		t.Fatalf("the directory-format dump: %+v", b)
	}
}

// Without the backups before an update ([backups] pre_update = "off") the backup step
// takes none.
func TestRehearserBackupBeforeOff(t *testing.T) {
	r := rehearser{t: dbsafe.Target{Backups: t.TempDir()}, off: true}
	b, err := r.BackupBefore(t.Context(), "1.4.0", "1.5.0", nil)
	if err != nil || b != nil {
		t.Fatalf("backup = %+v, %v", b, err)
	}
	if _, ok := r.Pending(t.Context(), ""); ok {
		t.Fatal("no migrations folder: whether the release adds any cannot be told")
	}
}
