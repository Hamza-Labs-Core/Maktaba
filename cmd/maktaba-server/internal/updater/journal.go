// Package updater is a machine's own part of an automatic update (Epic 35 stories 35.4,
// 35.5, 35.7, 35.12; specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md): the
// home's API orders this machine's supervisor to prepare a release (fetch and verify it,
// stage it, smoke-test it, on the home rehearse its migrations) and then to apply it
// (switch to it and restart). The release that comes up next — the new one, or the
// previous one after a rollback — finishes the run: the new one watches its health and
// commits, or rolls back; the previous one records the rollback.
//
// The journal (<updates>/journal.json, rewritten atomically before and after every step)
// is the contract between the two releases and outlives a power cut: Resume reads it at
// every start. How a release is staged and switched to depends on the kind of install
// (Installer: the side-by-side layout, the Linux runtime copy, the desktop app's bundle).
package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// States of a machine's run (Journal.State).
const (
	StatePreparing     = "preparing"      // fetching, staging, checking: nothing changed
	StatePrepared      = "prepared"       // staged and checked; waits for apply
	StatePrepareFailed = "prepare_failed" // nothing changed
	StateAborted       = "aborted"        // stopped before the switch; nothing changed
	StateApplying      = "applying"       // switching to the staged release
	StateRestarting    = "restarting"     // switched; the machine restarts on it
	StateWatching      = "watching"       // the new release runs; its health is watched
	StateCommitted     = "committed"      // the new release stays
	StateRollingBack   = "rolling_back"   // switched back; the previous release finishes
	StateRolledBack    = "rolled_back"    // the previous release runs again
	StateApplyFailed   = "apply_failed"   // failed before the restart; the previous release runs
	StateFailed        = "failed"         // a rollback that could not be made: needs a person
)

// Steps of a run (Journal.Step, StepRecord.Step).
const (
	StepPreflight = "preflight"
	StepStage     = "stage"
	StepSmoke     = "smoke"
	StepRehearse  = "rehearse"
	StepSwitch    = "switch"
	StepRestart   = "restart"
	StepWatch     = "watch"
	StepCommit    = "commit"
	StepRollback  = "rollback"
)

// Busy reports whether state is a run under way (another may not start).
func Busy(state string) bool {
	switch state {
	case StatePreparing, StateApplying, StateRestarting, StateWatching, StateRollingBack:
		return true
	}
	return false
}

// Ended reports whether state is the end of a run (its outcome is known).
func Ended(state string) bool {
	switch state {
	case StatePrepareFailed, StateAborted, StateCommitted, StateRolledBack, StateApplyFailed, StateFailed:
		return true
	}
	return false
}

// Job is what the home orders a machine to install. Nothing in it names a path: the
// release is the verified release.json's, downloaded from the asset URLs of the pinned
// repository's release (a node verifies everything itself).
type Job struct {
	RunID   string            `json:"run_id"`
	Version string            `json:"version"`
	Tag     string            `json:"tag"`
	Assets  map[string]string `json:"assets"`
	// Home: the home's own update (rehearse the migrations; its database is migrated by
	// the new release's database step at its start).
	Home bool `json:"home,omitempty"`
	// Rehearse the migrations on a scratch copy (rules.updates.rehearse_migrations),
	// skipped above RehearseMaxGB.
	Rehearse      bool `json:"rehearse,omitempty"`
	RehearseMaxGB int  `json:"rehearse_max_db_gb,omitempty"`
	// KeepVersions releases stay installed after a commit (never fewer than 2).
	KeepVersions int `json:"keep_versions,omitempty"`
	// WatchSeconds: how long the new release must stay healthy before it is
	// committed; HealthSeconds: how long it may stay unhealthy before it is rolled
	// back (0: the defaults).
	WatchSeconds  int `json:"watch_seconds,omitempty"`
	HealthSeconds int `json:"health_seconds,omitempty"`
}

// Validate checks the job's shape.
func (j Job) Validate() error {
	switch {
	case j.RunID == "":
		return errors.New("run_id is missing")
	case !layout.ValidVersion(j.Version):
		return fmt.Errorf("bad version %q", j.Version)
	case j.Tag != "v"+layout.NormalizeVersion(j.Version):
		return fmt.Errorf("tag %q is not v%s", j.Tag, j.Version)
	}
	return nil
}

// Default timings of the watch (design §5.3 step 13, §5.4; a node's apply, §13.3).
const (
	DefaultWatchHome   = 10 * time.Minute
	DefaultWatchNode   = 2 * time.Minute
	DefaultHealthLimit = 3 * time.Minute
)

// Watch is how long the new release must stay healthy.
func (j Job) Watch() time.Duration {
	if j.WatchSeconds > 0 {
		return time.Duration(j.WatchSeconds) * time.Second
	}
	if j.Home {
		return DefaultWatchHome
	}
	return DefaultWatchNode
}

// HealthLimit is how long it may stay unhealthy.
func (j Job) HealthLimit() time.Duration {
	if j.HealthSeconds > 0 {
		return time.Duration(j.HealthSeconds) * time.Second
	}
	return DefaultHealthLimit
}

// Failure is why a step failed: a code (the Updates page translates it) and the
// programs' own words.
type Failure struct {
	Step   string `json:"step"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func (f *Failure) Error() string {
	if f.Detail == "" {
		return f.Code
	}
	return f.Code + ": " + f.Detail
}

// Fail is a Failure as an error.
func Fail(code, format string, args ...any) error {
	return &Failure{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// StepRecord is one step's timing and outcome.
type StepRecord struct {
	Step       string     `json:"step"`
	State      string     `json:"state"` // running | done | failed | skipped
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Detail     string     `json:"detail,omitempty"`
}

// Rehearsal is what the rehearsal measured.
type Rehearsal struct {
	Skipped    string  `json:"skipped,omitempty"` // why it did not run
	DBBytes    int64   `json:"db_bytes,omitempty"`
	MigrateSec float64 `json:"migrate_seconds,omitempty"`
	Pending    int     `json:"pending,omitempty"` // migrations the release adds
}

// Journal is a machine's run, as <updates>/journal.json holds it.
type Journal struct {
	V     int    `json:"v"`
	Job   Job    `json:"job"`
	Kind  string `json:"kind"` // the installer's
	From  string `json:"from"` // the release that ran when the run was prepared
	State string `json:"state"`
	Step  string `json:"step,omitempty"`
	// Steps are every step's record, in order.
	Steps []StepRecord `json:"steps"`
	Error *Failure     `json:"error,omitempty"`
	// Staged and FromRef are the installer's handles of the new and the previous
	// release (a directory, a runtime's name, a kept payload).
	Staged    string     `json:"staged,omitempty"`
	FromRef   string     `json:"from_ref,omitempty"`
	Rehearsal *Rehearsal `json:"rehearsal,omitempty"`
	// WatchSince is when the new release first came up; Starts how often it started
	// while watched (a supervisor that keeps dying rolls back).
	WatchSince *time.Time `json:"watch_since,omitempty"`
	Starts     int        `json:"starts,omitempty"`
	// Reason is why the run rolled back.
	Reason    string    `json:"reason,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// journalVersion is Journal.V.
const journalVersion = 1

// JournalFile is the journal's name in the updates directory.
const JournalFile = "journal.json"

// clone is a deep enough copy for a reader.
func (j *Journal) clone() *Journal {
	if j == nil {
		return nil
	}
	c := *j
	c.Steps = append([]StepRecord(nil), j.Steps...)
	if j.Error != nil {
		e := *j.Error
		c.Error = &e
	}
	if j.Rehearsal != nil {
		r := *j.Rehearsal
		c.Rehearsal = &r
	}
	c.Job.Assets = make(map[string]string, len(j.Job.Assets))
	for k, v := range j.Job.Assets {
		c.Job.Assets[k] = v
	}
	return &c
}

// LoadJournal reads dir/journal.json; nil when there is none. A journal that cannot be
// parsed is an error (the caller sets it aside: it is never guessed at).
func LoadJournal(dir string) (*Journal, error) {
	b, err := os.ReadFile(filepath.Join(dir, JournalFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, JournalFile), err)
	}
	if j.V != journalVersion {
		return nil, fmt.Errorf("%s: journal version %d, this release reads %d", filepath.Join(dir, JournalFile), j.V, journalVersion)
	}
	return &j, nil
}

// SaveJournal writes j to dir/journal.json atomically (a power cut leaves the old or the
// new journal, never half of one).
func SaveJournal(dir string, j *Journal) error {
	j.V = journalVersion
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return layout.WriteFileAtomic(filepath.Join(dir, JournalFile), append(b, '\n'), 0o600)
}

// setAside moves an unreadable journal out of the way, keeping it for a person.
func setAside(dir string, now time.Time) error {
	from := filepath.Join(dir, JournalFile)
	return os.Rename(from, from+".unreadable-"+now.UTC().Format("20060102T150405"))
}
