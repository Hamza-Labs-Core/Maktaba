package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbsafe"
)

// The dump a release's start took before its migrations is found whichever way the start
// named the versions (with or without their v): the run records it.
func TestRehearserFindsTheUpdateDump(t *testing.T) {
	dir := t.TempDir()
	r := rehearser{dbsafe.Target{Backups: dir}}
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
}
