package pgbundle

import (
	"fmt"
	"os"
	"path/filepath"
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

// NoRoomError says a dump was not started: even with the old dumps gone it would
// leave its disk without the reserve.
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), backupSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // gone meanwhile
		}
		d := dump{filepath.Join(dir, e.Name()), uint64(info.Size()), info.ModTime()}
		all = append(all, d)
		used += d.size
		k := dumpKind(e.Name())
		if n, ok := newest[k]; !ok || d.mod.After(n.mod) {
			newest[k] = d
		}
	}
	var next uint64
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
		if newest[dumpKind(filepath.Base(d.path))].path == d.path {
			continue
		}
		if err := os.Remove(d.path); err != nil && !os.IsNotExist(err) {
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
