package updater

import (
	"context"
	"fmt"
	"time"
)

// The home's pre-update dump, on the side (the owner, 2026-10-27: an update never blocks
// the library for a backup). An apply on the home first asks the database how many of the
// staged release's migrations it lacks. None: no dump at all; the switch is a restart, and
// so is going back from it (the release before knows the database as it is). Some, or
// that cannot be told: the running release dumps the database while it goes on serving
// (pg_dump reads one snapshot), the step's detail saying how much is written, and only the
// switch waits for it. The dump is noted in the database's record: the new release's start
// takes it as its pre-update dump and migrates at once (serve_appbundle.go). Writes made
// between the dump's start and the switch are not in it (a minute or two, the cluster's
// jobs drained first).

// SideBackup is a Rehearser that takes the home's pre-update dump while the running
// release serves.
type SideBackup interface {
	// Pending counts the migrations of the folder dir (a staged release's) the database
	// does not hold; ok false when that cannot be told.
	Pending(ctx context.Context, dir string) (n int, ok bool)
	// BackupBefore dumps the database for the update from → to beside the backups,
	// calling progress with the bytes written so far now and then; nil with no error when
	// the backups before an update are off ([backups] pre_update = "off").
	BackupBefore(ctx context.Context, from, to string, progress func(bytes int64)) (*Backup, error)
}

// NextRecorder is a Records that notes, in the database's record, the dump the running
// release took for the next one (its start takes it instead of dumping).
type NextRecorder interface {
	NoteNext(running, next, dump string) error
}

// backupBeforeSwitch is the apply's backup step on the home (the comment above); an error
// means nothing was switched.
func (m *Machine) backupBeforeSwitch(ctx context.Context) error {
	j := m.Status().Journal
	b, ok := m.Rehearser.(SideBackup)
	if j == nil || !j.Job.Home || !ok {
		return nil
	}
	_ = m.beginStep(StepBackup)
	_, migrations := m.Installer.Tools(j)
	if n, known := b.Pending(ctx, migrations); known && n == 0 {
		_ = m.endStep(StepBackup, "skipped", "skipped: "+j.Job.Version+" adds no migration, so no backup: the switch is a restart")
		m.logf("%s adds no migration: no backup, the switch is a restart", j.Job.Version)
		return nil
	} else if known {
		m.logf("%s adds %d migration(s): saving the library database first, while it goes on serving", j.Job.Version, n)
	} else {
		m.logf("whether %s adds migrations cannot be told: saving the library database first, while it goes on serving",
			j.Job.Version)
	}
	start := m.now()
	bk, err := b.BackupBefore(ctx, j.From, j.Job.Version, func(n int64) {
		m.stepDetail(StepBackup, fmt.Sprintf("%d MB written", n>>20))
	})
	if err != nil {
		f := failure(StepBackup, err)
		if f.Code == StepBackup+"_failed" {
			f.Code = "db_dump_failed"
		}
		_ = m.endStep(StepBackup, "failed", f.Error())
		return f
	}
	if bk == nil {
		_ = m.endStep(StepBackup, "skipped", "skipped: no backup before an update ([backups] pre_update = \"off\")")
		return nil
	}
	if r, ok := m.Records.(NextRecorder); ok {
		if err := r.NoteNext(j.From, j.Job.Version, bk.Path); err != nil {
			// the new release's start then dumps by itself, as it did before
			m.warnf("the backup %s cannot be noted for %s's start: %v", bk.Path, j.Job.Version, err)
		}
	}
	took := m.now().Sub(start).Round(time.Second)
	_ = m.mutate(func(j *Journal) { j.Backup = bk })
	_ = m.endStep(StepBackup, "done", fmt.Sprintf("%d MB in %s", bk.Bytes>>20, took))
	m.logf("the library database is saved in %s (%d MB in %s)", bk.Path, bk.Bytes>>20, took)
	return nil
}

// stepDetail says what the step under way has done so far.
func (m *Machine) stepDetail(step, detail string) {
	_ = m.mutate(func(j *Journal) {
		for i := len(j.Steps) - 1; i >= 0; i-- {
			if j.Steps[i].Step == step && j.Steps[i].FinishedAt == nil {
				j.Steps[i].Detail = detail
				return
			}
		}
	})
}
