package pgbundle

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The desktop app's updates (Epic 38 story 38.12): before an update's
// migrations the server dumps the database, backups/pre-<old>-to-<new>.dump,
// the way back to the old version; when the migrations fail it restores that
// dump into the cluster its postgres role runs.

// updateDumpPrefix starts every pre-update dump's name (the nightly ones start
// with backupPrefix: the two are kept apart).
const updateDumpPrefix = "pre-"

// unsafeInName is what a version may not carry into a file name.
var unsafeInName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// UpdateDumpName is the pre-update dump's file name for an update from → to
// ("unknown" for a from that is not known: an install from before the
// server kept the record).
func UpdateDumpName(from, to string) string {
	if from == "" {
		from = "unknown"
	}
	return updateDumpPrefix + unsafeInName.ReplaceAllString(from, "_") + "-to-" +
		unsafeInName.ReplaceAllString(to, "_") + backupSuffix
}

// DumpBeforeUpdate writes a pg_dump custom-format dump of the app database into
// backups/UpdateDumpName(from, to) (beside and renamed into place: a dump of
// that name there already is replaced, so the dump is always the database as
// it is now) and keeps the newest keep pre-update dumps, this one always. Room
// is made for it first (MakeRoom). The server must be up.
func (b Bundle) DumpBeforeUpdate(ctx context.Context, from, to string, keep int) (string, error) {
	if err := MakeRoomWith(b.Backups, b.room()); err != nil {
		return "", err
	}
	path := filepath.Join(b.Backups, UpdateDumpName(from, to))
	if err := b.dumpTo(ctx, path); err != nil {
		return "", err
	}
	return path, PruneBackups(b.Backups, path, BackupsKeep(b.Backups, keep))
}

// RestoreRunning replaces the app database with a dump of this cluster's own (a
// pre-update dump) on the server already running (the supervisor's postgres
// role): what is connected to the database is cut off, the database is made
// afresh and the dump restored as Restore does. The desktop app's server does
// this when an update's migrations failed, before any other role started.
func (b Bundle) RestoreRunning(ctx context.Context, dump string, log io.Writer) error {
	if _, err := os.Stat(dump); err != nil {
		return err
	}
	q, err := b.NewQuerier()
	if err != nil {
		return err
	}
	return b.restoreInto(ctx, q, dump, RestoreOptions{}, log)
}

// ClusterMajor is the Postgres major version that made the cluster (its
// PG_VERSION file), "" before initdb.
func (b Bundle) ClusterMajor() (string, error) {
	raw, err := os.ReadFile(filepath.Join(b.Data, "PG_VERSION"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
