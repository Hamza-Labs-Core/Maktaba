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
	"encoding/json"
	"errors"
	"fmt"
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
}

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
	// KeptFor is the older release a rollback left this database for with nothing put
	// back (layout.MarkRecordKept): Version's migrations were all rollback-safe, so KeptFor
	// starts on the newer schema (its api told it may: MAKTABA_SCHEMA_AHEAD_OK), and
	// KeptReason is why the newer release rolled back. Version stays the schema's release.
	KeptFor    string `json:"kept_for,omitempty"`
	KeptReason string `json:"kept_reason,omitempty"`
	// DumpKeptFor is KeptFor of the database Dump was taken of (an update from a release
	// running on a kept, newer schema): a restore of Dump is again that schema, kept for
	// that release, and says so (KeptFor of the restored record).
	DumpKeptFor string    `json:"dump_kept_for,omitempty"`
	At          time.Time `json:"at"`
}

// Ran is the release that last started on the database: Release, else Version.
func (d Database) Ran() string {
	if d.Release != "" {
		return d.Release
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
)

// DatabasePath is the database's record.
func DatabasePath(stateDir string) string { return filepath.Join(stateDir, "state", "database.json") }

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
	return d, true, nil
}

// WriteDatabase saves the database's record (whole, atomically), stamped now.
func WriteDatabase(stateDir string, d Database) error {
	d.At = time.Now().UTC()
	raw, err := json.MarshalIndent(d, "", "  ")
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
