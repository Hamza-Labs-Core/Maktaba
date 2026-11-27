package pgbundle

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// A dump is as big as the database (tens of GB once the search vectors live in it),
// and the backups folder sits on the disk the database and the owner's own files use.
// Before a dump is written MakeRoom sees that it fits: old dumps go, oldest first,
// until the dump leaves the disk's reserve free and the backups stay within their
// share of the disk; the newest dump of each kind always stays. A dump that cannot
// fit even so is not started (NoRoomError): a disk filled to the last byte stops
// Postgres itself.

// diskSpace is the free (to this user) and total bytes of the disk holding dir
// (tests replace it).
var diskSpace = diskSpaceOf

// reserveShare and reserveMin: a dump leaves free 5% of its disk, at least 2 GiB
// (what Postgres needs to go on writing its log; tests lower it).
const reserveShare = 20 // 1/20

var reserveMin uint64 = 2 << 30

// backupsShare: the dumps together take at most a fifth of their disk (the
// newest of each kind always stays, whatever its size).
const backupsShare = 5 // 1/5

// dumpGrowth is the margin on the newest dump's size for the next one (the database
// grows between two dumps).
const dumpGrowth = 1.25

// RoomPolicy is what a dump leaves on its disk ([backups] reserve_percent,
// reserve_min_gb, max_share_percent: Epic 36 story 36.16).
type RoomPolicy struct {
	// ReserveShare is the share of the disk a dump leaves free (0..1).
	ReserveShare float64
	// ReserveMin is the least it leaves free.
	ReserveMin uint64
	// MaxShare is the most of the disk the dumps take together (0..1).
	MaxShare float64
}

// DefaultRoom is the room a dump leaves when nothing is set: 5% of the disk, at least
// 2 GiB, the dumps within a fifth of it.
func DefaultRoom() RoomPolicy {
	return RoomPolicy{ReserveShare: 1.0 / reserveShare, ReserveMin: reserveMin, MaxShare: 1.0 / backupsShare}
}

func (p RoomPolicy) reserveOf(total uint64) uint64 {
	return max(uint64(float64(total)*p.ReserveShare), p.ReserveMin)
}

func (p RoomPolicy) budget(total uint64) uint64 {
	share := p.MaxShare
	if share <= 0 || share > 1 {
		share = 1.0 / backupsShare
	}
	return uint64(float64(total) * share)
}

// NoRoomError says a dump was not started: it would leave its disk without the reserve,
// with every old dump gone that may go.
type NoRoomError struct {
	Dir        string
	Free, Need uint64
}

func (e *NoRoomError) Error() string {
	return fmt.Sprintf("not enough free space for the database dump in %s: %d MB free, %d MB needed "+
		"(the dump and the reserve the disk keeps free); free some space on that disk or move "+
		"Maktaba's data to a bigger one", e.Dir, e.Free>>20, e.Need>>20)
}

// dumpKind is the group a dump's name puts it in: the newest of each stays.
func dumpKind(name string) string {
	for _, p := range []string{backupPrefix, updateDumpPrefix, "failed-", ReplacedDumpPrefix} {
		if strings.HasPrefix(name, p) {
			return p
		}
	}
	return ""
}

// MakeRoom makes room in dir (a backups folder) for the next dump, its size guessed
// from the newest finished dump there: it removes the oldest finished dumps (never
// the newest of a kind) while the dump would leave less than the disk's reserve
// free or the dumps would take more than their share of the disk, and returns a
// *NoRoomError when the reserve cannot be kept even so.
func MakeRoom(dir string) error { return MakeRoomWith(dir, DefaultRoom()) }

// MakeRoomWith is MakeRoom with the room p leaves.
func MakeRoomWith(dir string, p RoomPolicy) error {
	held := BackupsHeld(dir) // a restore is not done: every dump stays
	return roomIn(dir, p, 0, func(string) bool { return held }, RemoveDump)
}

// MakeRoomKeeping is MakeRoomWith for the copy of the database a restore replaces, while the
// backups are held for it (HoldBackups): the dumps of names stay, with the newest of each
// kind, and no others. When that is not room enough nothing is removed.
func MakeRoomKeeping(dir string, p RoomPolicy, names ...string) error {
	if err := RoomKeeping(dir, p, names...); err != nil {
		return err
	}
	return roomIn(dir, p, 0, keepNames(names), RemoveDump)
}

// RoomKeeping says whether MakeRoomKeeping would make the room (a *NoRoomError: it would
// not), removing nothing.
func RoomKeeping(dir string, p RoomPolicy, names ...string) error {
	return roomIn(dir, p, 0, keepNames(names), func(string) error { return nil })
}

// RoomNow says whether there is room for the next dump as it is, removing nothing.
func RoomNow(dir string, p RoomPolicy) error {
	return RoomNowAtLeast(dir, p, 0)
}

// RoomNowAtLeast is RoomNow with a floor minNext on the next dump's estimated size
// (for example, the live database's current size when it has grown past the newest backup).
func RoomNowAtLeast(dir string, p RoomPolicy, minNext uint64) error {
	return roomIn(dir, p, minNext, func(string) bool { return true }, RemoveDump)
}

// keepNames keeps names (a disk may not tell their case apart).
func keepNames(names []string) func(string) bool {
	return func(name string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) })
	}
}

// roomIn is MakeRoomWith's work: minNext is a floor on the next dump's estimated size,
// keep says which dumps stay besides the newest of each kind, remove removes one.
func roomIn(dir string, p RoomPolicy, minNext uint64, keep func(name string) bool, remove func(path string) error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	free, total, err := diskSpace(dir)
	if err != nil {
		return nil // a disk that cannot be read is no reason to skip a backup
	}
	type dump struct {
		path string
		size uint64
		mod  time.Time
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var all []dump
	var used uint64
	newest := map[string]dump{}
	for _, e := range entries {
		if !isDump(e) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // gone meanwhile
		}
		p := filepath.Join(dir, e.Name())
		d := dump{p, uint64(DumpBytes(p)), dumpAt(e.Name(), info.ModTime())}
		all = append(all, d)
		used += d.size
		k := dumpKind(e.Name())
		if n, ok := newest[k]; !ok || d.mod.After(n.mod) {
			newest[k] = d
		}
	}
	next := minNext
	for _, d := range newest {
		if n := uint64(float64(d.size) * dumpGrowth); n > next {
			next = n
		}
	}
	need := next + p.reserveOf(total)
	budget := p.budget(total)
	sort.Slice(all, func(i, j int) bool { return all[i].mod.Before(all[j].mod) })
	for _, d := range all {
		if free >= need && used+next <= budget {
			break
		}
		if name := filepath.Base(d.path); newest[dumpKind(name)].path == d.path || keep(name) {
			continue
		}
		if err := remove(d.path); err != nil {
			return err
		}
		free += d.size
		used -= d.size
	}
	if free < need {
		return &NoRoomError{Dir: dir, Free: free, Need: need}
	}
	return nil
}
