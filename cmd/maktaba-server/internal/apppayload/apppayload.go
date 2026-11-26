// Package apppayload reads the desktop app's payload.json (what the server
// payload carries: its version, the Postgres, Python and pipeline inside) and
// keeps two records under <state>/state (Epic 38 story 38.12, Epic 36's
// contract-update.md):
//
//   - database.json, the release whose migrations the library database holds,
//     written at the server's start before any role runs: a new version dumps
//     the database before its migrations, an older one is refused;
//   - payload.json, the copy of the last payload that ran healthy (three
//     minutes, every process up).
package apppayload

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Payload is the part of payload.json the server reads.
type Payload struct {
	Schema   int    `json:"schema"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Postgres struct {
		Version string `json:"version"`
	} `json:"postgres"`
	Python struct {
		Version string `json:"version"`
	} `json:"python"`
	Pipeline struct {
		Wheel             string `json:"wheel"`
		SHA256            string `json:"sha256"`
		ConstraintsSHA256 string `json:"constraints_sha256"`
	} `json:"pipeline"`
	// Flavour is "full" or "core" (story 51.17: the Mac's Core dmg leaves out ollama);
	// empty, a payload from before, is full.
	Flavour string `json:"flavour,omitempty"`
}

// Core reports whether the payload is the core flavour.
func (p Payload) Core() bool { return strings.TrimSpace(p.Flavour) == "core" }

// PostgresMajor is the bundled Postgres's major version ("16" of "16.15.0").
func (p Payload) PostgresMajor() string {
	major, _, _ := strings.Cut(p.Postgres.Version, ".")
	return major
}

// Read reads a payload.json.
func Read(path string) (Payload, error) {
	var p Payload
	raw, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// StatePath is the copy of the last payload that ran healthy.
func StatePath(stateDir string) string { return filepath.Join(stateDir, "state", "payload.json") }

// Confirmed is the last payload that ran healthy; ok false before the first.
func Confirmed(stateDir string) (Payload, bool, error) {
	p, err := Read(StatePath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return Payload{}, false, nil
	}
	return p, err == nil, err
}

// Confirm saves payloadDir's payload.json as the last one that ran healthy.
func Confirm(stateDir, payloadDir string) error {
	raw, err := os.ReadFile(filepath.Join(payloadDir, "payload.json"))
	if err != nil {
		return err
	}
	return layout.WriteFileAtomic(StatePath(stateDir), raw, 0o644)
}

// Database is the record of the release whose migrations the library database
// holds, <state>/state/database.json. The server writes it at its start before
// any role runs (serve_appbundle.go): when it has dumped the database before an
// update's migrations (migrating), once they ran (migrated), and around putting
// the dump back when they failed (restoring, then restored).
type Database struct {
	// Version is the release whose migrations the database holds: once
	// migrating, the release being updated to (its migrations may have run in
	// part); once restored, the release from before the failed update. "" when
	// that is not known (an install from before this record). A release that adds
	// no migration leaves it as it was (Release says it runs): the release before
	// it knows the database as well, and runs on it again with no dump put back.
	Version string `json:"version"`
	State   string `json:"state"`
	// Release is the release that last started on the database when it is not
	// Version (an update that added no migration); "": Version.
	Release string `json:"release,omitempty"`
	// From is the release before the update and Dump the dump taken before its
	// migrations (backups/pre-<from>-to-<version>.dump).
	From string `json:"from,omitempty"`
	Dump string `json:"dump,omitempty"`
	// Unconfirmed is how Dump is written until the update's release is confirmed (its
	// watch passed: Confirmed; ReadDatabase gives it as Dump). The guard of every launcher
	// and the rollback of every release before 2026-11-24 mark a record whose release they
	// give up on restoring, and the release before then puts the whole dump back unasked;
	// all of them leave a record with no dump alone (MarkRecordRestoring, since the first
	// release with a guard). So a new release under an older launcher, or rolled back to an
	// older release, names its backup where only the releases that stop and ask read it:
	// the older release refuses the database as newer than itself and stops
	// (db_refused_older), with nothing put back.
	Unconfirmed string `json:"dump_unconfirmed,omitempty"`
	Confirmed   bool   `json:"confirmed,omitempty"`
	// Next is the release the running one switches to and NextDump the dump it took
	// for it while it served, at NextAt (the update's backup step: internal/updater):
	// Next's start takes it as its pre-update dump instead of taking one.
	Next     string     `json:"next,omitempty"`
	NextDump string     `json:"next_dump,omitempty"`
	NextAt   *time.Time `json:"next_at,omitempty"`
	// Failed is the release whose migrations failed (restoring, restored) and
	// Error what they said.
	Failed string `json:"failed,omitempty"`
	Error  string `json:"error,omitempty"`
	// By is who asked for a restore by hand (story 35.5 AC8: an admin on the Updates page,
	// or the command line): restoring, then restored, with no failed release; Version is
	// then the release whose database is replaced and From the one the dump is put back
	// for.
	By string `json:"by,omitempty"`
	// Before is the record that restore replaced (nil: asked on a release from before
	// 2026-11-25), and Data and DataID what is known of the data since: Data one of the
	// Data* states, written before each step that changes it, DataID the identity of the
	// database the restore found there (dbsafe's Identity), which proves that it is still
	// that one, whatever ran since. Every way out of a restore that cannot finish is allowed
	// or refused by them alone (serve_restore_exits.go). Aside is the dump the replaced
	// database was set aside in (once for a restore, however often it is tried),
	// RestoreError why its last try failed and TriedBy the release that tried.
	Before *Database `json:"before,omitempty"`
	Data   string    `json:"data,omitempty"`
	DataID string    `json:"data_id,omitempty"`
	// DataTable is the rest of that identity: the migrations' own table, which a restore that
	// leaves the public schema where it is makes again (apart from DataID: a release that does
	// not know of it goes by that alone, which such a restore marks before its commit).
	DataTable string `json:"data_table,omitempty"`
	// MaybeSchemas are the schemas of the backups asked before this one while the data was not
	// known (0: one whose schema cannot be told): any of them may be what is there.
	MaybeSchemas []int64 `json:"maybe_schemas,omitempty"`
	Aside        string  `json:"aside,omitempty"`
	// AsideBytes is Aside's size: another, or none, is not that copy.
	AsideBytes int64 `json:"aside_bytes,omitempty"`
	// NoCopy: the person confirmed a restore with no copy kept of the database it replaces:
	// when there is no room for one (NoCopyRoom), or whatever the last try said kept it from
	// being taken (NoCopyAny); CopyError why its last try could not keep one (that try
	// stopped before it touched anything).
	NoCopy       string `json:"no_copy,omitempty"`
	CopyError    string `json:"copy_error,omitempty"`
	RestoreError string `json:"restore_error,omitempty"`
	TriedBy      string `json:"tried_by,omitempty"`
	// KeptFor is the older release a rollback left this database for with nothing put
	// back (layout.MarkRecordKept): Version's migrations were all rollback-safe, so KeptFor
	// starts on the newer schema (its api told it may: MAKTABA_SCHEMA_AHEAD_OK), and
	// KeptReason is why the newer release rolled back. Version stays the schema's release
	// unless a Down rollback or restore reset it (in which case SchemaRelease names the
	// schema's own release).
	KeptFor    string `json:"kept_for,omitempty"`
	KeptReason string `json:"kept_reason,omitempty"`
	// SchemaRelease is the release whose migrations the database actually holds when
	// Version was reset to an older release on a kept schema (a Down rollback or a restore
	// of a dump taken from a kept schema): keptFor accepts releases between KeptFor and
	// SchemaRelease. Empty on older records, where Version is the schema's release.
	SchemaRelease string `json:"schema_release,omitempty"`
	// DumpKeptFor is KeptFor of the database Dump was taken of (an update from a release
	// running on a kept, newer schema): a restore of Dump is again that schema, kept for
	// that release, and says so (KeptFor of the restored record). DumpSchemaRelease is the
	// schema's own release for that dump (SchemaRelease or Version).
	DumpKeptFor       string `json:"dump_kept_for,omitempty"`
	DumpSchemaRelease string `json:"dump_schema_release,omitempty"`
	// DownFrom is the release whose Down scripts brought the database back to Version's
	// schema (layout.MarkRecordDown; the database step, when that release's migrations
	// failed part way: updater.FailedBack), DownReason why.
	DownFrom   string `json:"down_from,omitempty"`
	DownReason string `json:"down_reason,omitempty"`
	// SchemaBefore is the newest migration the database recorded before the update's
	// migrations started (migrating; 0: not known): the slot a failed update's Down
	// scripts go back to (updater.FailedBack), whichever release ran on it.
	SchemaBefore int64 `json:"schema_before,omitempty"`
	// Held is why a failed update stopped instead of going back by itself (held: the
	// update attempt's code; no release puts a dump back by itself since 2026-11-24: db_down_failed, db_held_lossy, db_held_unknown, or
	// db_held_unasked for a restore mark no person asked for). "": marked held by a
	// rollback (the launcher's guard, the update's watch), whose reason is Error.
	Held string    `json:"held,omitempty"`
	At   time.Time `json:"at"`
}

// Ran is the release that last started on the database: Release, else Version.
func (d Database) Ran() string {
	if d.Release != "" {
		return d.Release
	}
	return d.Version
}

// SchemaOwner is the release whose migrations the database holds: SchemaRelease when set,
// else Version.
func (d Database) SchemaOwner() string {
	if d.SchemaRelease != "" {
		return d.SchemaRelease
	}
	return d.Version
}

// The states of a Database record.
const (
	// DatabaseMigrating: the dump is taken; the migrations have not finished.
	DatabaseMigrating = "migrating"
	// DatabaseMigrated: Version's migrations ran.
	DatabaseMigrated = "migrated"
	// DatabaseRestoring: Failed's migrations failed; Dump is being put back.
	DatabaseRestoring = "restoring"
	// DatabaseRestored: the database is as it was before the failed update.
	DatabaseRestored = "restored"
	// DatabaseHeld: Failed's migrations failed, or Failed was rolled back after they ran,
	// and neither keeping its schema nor its Down scripts took the database back. Nothing
	// is put back by itself: every start stops on it (db_held; a release older than
	// Version, the releases before this state included, refuses it: db_refused_older)
	// until a person chooses: put Dump back (`maktaba-server restore`, a restore asked:
	// restoring, By), or install a release newer than Failed, which goes on from it.
	DatabaseHeld = "held"
)

// What the data is while a restore a person asked for is not done (Database.Data).
const (
	// DataUnknown: no release that keeps track of it has looked yet, and what can be told
	// now does not say (the restore may have been tried by an older release).
	DataUnknown = ""
	// DataUntouched: the database as it was when the restore was asked.
	DataUntouched = "untouched"
	// DataReplaced: the backup's data is in its place, in part or whole, not known to be
	// whole (pgbundle.StepReplaced).
	DataReplaced = "replaced"
	// DataGone: dropped, or empty (pgbundle.StepGone).
	DataGone = "gone"
)

// What a person confirmed of the copy a restore keeps (Database.NoCopy).
const (
	NoCopyRoom = "space"
	NoCopyAny  = "error"
)

// DatabasePath is the database's record: the record of the cluster this install runs on
// (ClusterMajorIn), the same in every process (the server, the container's entrypoint, an
// offline restore, the updater).
func DatabasePath(stateDir string) string {
	return filepath.Join(stateDir, "state", ClusterRecordName(ClusterMajorIn(stateDir)))
}

// ClusterMajorIn is the major of the cluster an install under stateDir runs on, as its
// directories say (Epic 49 story 49.2): "18" when <stateDir>/postgres-18 holds a cluster,
// no hold keeps the previous major running (state/postgres-upgrade-held, pgbundle's
// HoldMajor), and the 16 cluster has not moved on since it was upgraded (a release before
// the upgrade ran on it again after a rollback: that release runs on 16 and reads
// database.json, and the next start of 18 upgrades it again). Else "" (<stateDir>/postgres,
// database.json, as every release before reads it). No program runs: the 16 cluster's
// pg_control is read as it is on disk.
func ClusterMajorIn(stateDir string) string {
	if _, err := os.Stat(filepath.Join(stateDir, "postgres-18", "PG_VERSION")); err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state", "postgres-upgrade-held")); err == nil {
		return ""
	}
	if oldMovedOn(stateDir) {
		return ""
	}
	return "18"
}

// oldMovedOn reports whether the 16 cluster beside an upgraded 18 one is no longer what
// the upgrade made it from: the upgrade's marker names no checkpoint (it did not finish),
// or the same cluster (its system identifier) has another checkpoint now. Another cluster
// in the old directory, or none, or an 18 cluster no upgrade made, is not taken over the
// 18 one (pgbundle.MajorUpgrade.Needed decides the same).
func oldMovedOn(stateDir string) bool {
	raw, err := os.ReadFile(filepath.Join(stateDir, "postgres-18", "maktaba-upgraded-from.json"))
	if err != nil {
		return false
	}
	var m struct {
		SystemID   string `json:"system_identifier"`
		Checkpoint string `json:"checkpoint"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	if m.Checkpoint == "" {
		return true
	}
	sysid, ckpt, ok := readControl(filepath.Join(stateDir, "postgres", "global", "pg_control"))
	if !ok || sysid != m.SystemID {
		return false
	}
	return ckpt != m.Checkpoint
}

// readControl is a cluster's pg_control's system identifier and latest checkpoint, as
// pg_controldata prints them (ControlFileData: uint64 system_identifier at 0, the latest
// checkpoint's XLogRecPtr at 32; the machine's byte order, little-endian on every platform
// Maktaba runs on).
func readControl(path string) (sysid, checkpoint string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	b := make([]byte, 40)
	if _, err := io.ReadFull(f, b); err != nil {
		return "", "", false
	}
	id := binary.LittleEndian.Uint64(b[0:8])
	lsn := binary.LittleEndian.Uint64(b[32:40])
	return strconv.FormatUint(id, 10), fmt.Sprintf("%X/%X", lsn>>32, lsn&0xffffffff), true
}

// ClusterRecordName is the record's file name for a cluster of major.
func ClusterRecordName(major string) string {
	if n, err := strconv.Atoi(major); err == nil && n >= 18 {
		return "database-pg" + major + ".json"
	}
	return "database.json"
}

// ReadDatabase reads the database's record; ok false when there is none.
func ReadDatabase(stateDir string) (Database, bool, error) {
	var d Database
	raw, err := os.ReadFile(DatabasePath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, false, fmt.Errorf("%s: %w", DatabasePath(stateDir), err)
	}
	return d.named(), true, nil
}

// named is d with the dump it withholds from older code (Unconfirmed) as its Dump.
func (d Database) named() Database {
	if d.Dump == "" {
		d.Dump = d.Unconfirmed
	}
	d.Unconfirmed = ""
	return d
}

// withheld is d as it is written: an update's dump not named where older code would put it
// back unasked (Unconfirmed), while its release is not confirmed.
func (d Database) withheld() Database {
	if !d.Confirmed && d.Dump != "" && (d.State == DatabaseMigrating || d.State == DatabaseMigrated) {
		d.Unconfirmed, d.Dump = d.Dump, ""
	}
	return d
}

// WriteDatabase saves the database's record (whole, atomically), stamped now.
func WriteDatabase(stateDir string, d Database) error {
	d.At = time.Now().UTC()
	raw, err := json.MarshalIndent(d.withheld(), "", "  ")
	if err != nil {
		return err
	}
	return layout.WriteFileAtomic(DatabasePath(stateDir), append(raw, '\n'), 0o644)
}

// Older reports whether release a comes before release b; false when the order
// cannot be told (a development build: never refused).
func Older(a, b string) bool {
	cmp, known := CompareVersions(a, b)
	return known && cmp < 0
}

// release is a release tag: v1.2.3, or a pre-release v1.2.3-rc.1 (as
// tools/release payload scripts accept them).
var release = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-(rc|alpha|beta|a|b)\.?(\d+))?$`)

// CompareVersions orders two release versions (-1, 0, 1); known false when
// either is not a release (a development build).
func CompareVersions(a, b string) (int, bool) {
	pa, ok1 := parse(a)
	pb, ok2 := parse(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	for i := range pa {
		switch {
		case pa[i] < pb[i]:
			return -1, true
		case pa[i] > pb[i]:
			return 1, true
		}
	}
	return 0, true
}

// parse: major, minor, patch, then the pre-release's rank (alpha 1, beta 2,
// rc 3, a release 4) and number.
func parse(v string) ([5]int, bool) {
	m := release.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [5]int{}, false
	}
	var out [5]int
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	switch m[4] {
	case "":
		out[3] = 4
	case "alpha", "a":
		out[3] = 1
	case "beta", "b":
		out[3] = 2
	case "rc":
		out[3] = 3
	}
	out[4], _ = strconv.Atoi(m[5])
	return out, true
}
