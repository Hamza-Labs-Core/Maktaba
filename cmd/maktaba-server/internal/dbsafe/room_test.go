package dbsafe

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// T06 (story 59.12): a data volume with less free space than the runtime and the dump
// (the database × 1.2) need: the update waits with the message, the need and the free space
// named; with room it goes on; an unknown free space passes.
func TestUpdateRoomForAContainer(t *testing.T) {
	data := t.TempDir()
	backups, runtime := filepath.Join(data, "backups"), filepath.Join(data, "runtime")
	const db = 10 << 30 // need: 12 GiB of dump and 1 GiB of runtime
	free := func(n uint64) func(string) uint64 { return func(string) uint64 { return n } }

	err := UpdateRoomFor(db, backups, runtime, free(12<<30))
	var short *ShortError
	if !errors.As(err, &short) {
		t.Fatalf("12 GiB free for 13 needed: %v", err)
	}
	if short.Need != 13<<30 || short.Free != 12<<30 {
		t.Fatalf("need %d, free %d", short.Need, short.Free)
	}
	if !strings.HasPrefix(err.Error(), "disk_short: Not enough free space on the data folder for a safe update: 13.0 GiB needed, 12.0 GiB free.") {
		t.Fatalf("said %q", err)
	}
	if err := UpdateRoomFor(db, backups, runtime, free(13<<30)); err != nil {
		t.Fatalf("13 GiB free: %v", err)
	}
	if err := UpdateRoomFor(db, backups, runtime, free(0)); err != nil {
		t.Fatalf("an unknown free space: %v", err)
	}
	// an empty database still needs the runtime's room
	if err := UpdateRoomFor(0, backups, runtime, free(512<<20)); !errors.As(err, &short) || short.Need != RuntimeRoom {
		t.Fatalf("no room for the runtime: %v", err)
	}
}
