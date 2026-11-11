package dbsafe

import (
	"context"
	"fmt"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// The room a container's update needs on its data volume (Epic 59 story 59.12): the new
// runtime (about 1 GB) and the pre-update dump (the database's size × 1.2), both on the data
// volume as a NAS has it. When the volume is short the update waits, saying so, and nothing
// is changed: the check is the preflight's, before anything is fetched.

// RuntimeRoom is what a new runtime takes on the volume.
const RuntimeRoom = 1 << 30

// ShortError is a data volume without room for a safe update. Its text starts with the
// failure's code (disk_short), as dbsafe's coded errors do.
type ShortError struct {
	Path       string
	Need, Free uint64
}

func (e *ShortError) Error() string {
	return fmt.Sprintf("disk_short: Not enough free space on the data folder for a safe update: %s needed, %s free. (%s)",
		gib(e.Need), gib(e.Free), e.Path)
}

// UpdateRoom checks the data volume has room for a container's update: the runtime under
// runtimeDir and the dump beside the backups (t.Backups), each on its own disk's account when
// they are on two. An unknown free space passes, as the other room checks do.
func (t Target) UpdateRoom(ctx context.Context, runtimeDir string) error {
	size, err := t.Size(ctx)
	if err != nil {
		return fmt.Errorf("db_unreachable: %w", err)
	}
	free := t.Free
	if free == nil {
		free = hostinfo.FreeDisk
	}
	return UpdateRoomFor(size, t.Backups, runtimeDir, free)
}

// UpdateRoomFor is UpdateRoom for a database of size bytes.
func UpdateRoomFor(size int64, backups, runtimeDir string, free func(string) uint64) error {
	if size < 0 {
		size = 0
	}
	dump := uint64(size) + uint64(size)/5
	need := map[string]uint64{}
	disk := func(path string) string { return mountOf(path) }
	need[disk(backups)] += dump
	need[disk(runtimeDir)] += RuntimeRoom
	for _, p := range []string{runtimeDir, backups} {
		if have, want := free(p), need[disk(p)]; have > 0 && have < want {
			return &ShortError{Path: p, Need: want, Free: have}
		}
	}
	return nil
}
