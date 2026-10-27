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
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// States of a machine's run (Journal.State).
const (
	StatePreparing     = "preparing"      // fetching, staging, checking: nothing changed
	StatePrepared      = "prepared"       // staged and checked; waits for apply
	StatePrepareFailed = "prepare_failed" // nothing changed
	StateAborted       = "aborted"        // stopped before the switch; nothing changed
	// An install its desktop app installs (Orderer): ordered from the app, which stages
	// the release and says when it installs (ordered); staged there, it waits for the
	// home's go (app_staged). No click on the machine (owner decision D1, 2026-10-27).
	// Nothing changed yet, and a newer run takes its place.
	StateOrdered     = "ordered"
	StateAppStaged   = "app_staged"
	StateApplying    = "applying"     // switching to the staged release
	StateRestarting  = "restarting"   // switched; the machine restarts on it
	StateWatching    = "watching"     // the new release runs; its health is watched
	StateCommitted   = "committed"    // the new release stays
	StateRollingBack = "rolling_back" // switched back; the previous release finishes
	StateRolledBack  = "rolled_back"  // the previous release runs again
	StateApplyFailed = "apply_failed" // failed before the restart; the previous release runs
	StateFailed      = "failed"       // a rollback that could not be made: needs a person
)

// Steps of a run (Journal.Step, StepRecord.Step).
const (
	StepPreflight = "preflight"
	StepStage     = "stage"
	StepSmoke     = "smoke"
	StepRehearse  = "rehearse"
	StepOrder     = "order" // the app's order, until the app staged the release
	StepSwitch    = "switch"
	StepRestart   = "restart"
	StepWatch     = "watch"
	StepCommit    = "commit"
	StepLauncher  = "launcher" // the launcher replaced (story 35.7 AC6)
	StepRollback  = "rollback"
	StepTV        = "tv" // the TV moved to the new streaming binary (story 35.6, tv.go)
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
	// BackupsKeep pre-update dumps (and as many dumps of failed updates) stay after a
	// commit (rules.updates.backups_keep; 0: the machine's own setting).
	BackupsKeep int `json:"backups_keep,omitempty"`
	// WatchSeconds: how long the new release must stay healthy before it is
	// committed; HealthSeconds: how long it may stay unhealthy before it is rolled
	// back (0: the defaults).
	WatchSeconds  int `json:"watch_seconds,omitempty"`
	HealthSeconds int `json:"health_seconds,omitempty"`
	// AppPostgresMajor is the release's app payloads' PostgreSQL majors, from the home's
	// verified catalog (release.json's app_postgres_major): another major than this
	// machine's is refused in preflight, before any download (and again from the verified
	// release.json at the stage)
	AppPostgresMajor map[string]int `json:"app_postgres_major,omitempty"`
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

// WatchBound is how long a watch may go on without a verdict (outside the start
// steps): a release whose health keeps flapping is rolled back after it.
func (j Job) WatchBound() time.Duration { return 3*j.Watch() + j.HealthLimit() }

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
	// App is the app's last answer to the order (ordered, staged).
	App string `json:"app,omitempty"`
	// InstallBy is the latest an automatic run gives the go (its window's end and overrun:
	// its apply says it): the app's notice names it ("restart by …"); nil: an admin's
	// run, which goes as soon as the app staged it and its turn comes.
	InstallBy *time.Time `json:"install_by,omitempty"`
	// LauncherProtocol is the release's launcher_protocol (its verified release.json).
	LauncherProtocol int `json:"launcher_protocol,omitempty"`
	// EdgeProtocol is the release's edge_protocol (its verified release.json): the TV
	// front door runs on (across the restart, on its previous release's binary) only to a
	// release that speaks its protocol, else it restarts (FrontDoorRestarts).
	EdgeProtocol int `json:"edge_protocol,omitempty"`
	// HandoverProtocol is the release's handover_protocol (its verified release.json): a
	// handover is made only onto a release whose supervisor adopts (story 35.7).
	HandoverProtocol int `json:"handover_protocol,omitempty"`
	// HandsOver: this machine's TV processes run on across its restart (story 35.7), so
	// the run need not wait for the TV to be quiet; HandoverWhy says why they cannot.
	HandsOver   bool   `json:"hands_over,omitempty"`
	HandoverWhy string `json:"handover_why,omitempty"`
	// Signed is the staged release's signed record (version, checksums digest, signing
	// time), kept at the commit for the next update's rules (an app bundle's, a runtime's:
	// a layout keeps its watermark)
	Signed *releaseverify.Watermark `json:"signed,omitempty"`
	// Backup is the dump the new release's start took before its migrations (a home's, at
	// its commit): the run records it (update_runs.backup)
	Backup *Backup `json:"backup,omitempty"`
	// WatchSince is when the new release first came up; Starts how often it started
	// while watched (a supervisor that keeps dying rolls back).
	WatchSince *time.Time `json:"watch_since,omitempty"`
	Starts     int        `json:"starts,omitempty"`
	// Pending is the start step the release that came up is busy with (its watch waits,
	// and so does the home's timeout of a node).
	Pending string `json:"pending,omitempty"`
	// WatchedSec is how long the watch has gone on outside the start steps (its bound);
	// PendingSec how long inside them (pendingCap).
	WatchedSec int `json:"watched_sec,omitempty"`
	PendingSec int `json:"pending_sec,omitempty"`
	// Rollbacks counts the switches back tried (a second one that does not take fails).
	Rollbacks int `json:"rollbacks,omitempty"`
	// TV is the TV step (the home's new release moves the TV to its streaming binary
	// before it commits, story 35.6); TVDeadline how long its change may wait behind
	// another (the window's end and overrun, from the home's apply).
	TV         *TVRun     `json:"tv,omitempty"`
	TVDeadline *time.Time `json:"tv_deadline,omitempty"`
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
	if j.TV != nil {
		t := *j.TV
		c.TV = &t
	}
	c.Job.Assets = make(map[string]string, len(j.Job.Assets))
	for k, v := range j.Job.Assets {
		c.Job.Assets[k] = v
	}
	return &c
}

// ErrJournalUnreadable: the journal is there but is no journal this release reads (it is
// set aside, never guessed at); any other error of LoadJournal is the file's (being
// replaced a moment, a reader holding it on Windows) and passes.
var ErrJournalUnreadable = errors.New("journal unreadable")

// LoadJournal reads dir/journal.json; nil when there is none. A read that fails is tried
// again a few times; a journal that cannot be parsed is ErrJournalUnreadable.
func LoadJournal(dir string) (*Journal, error) {
	path := filepath.Join(dir, JournalFile)
	var b []byte
	var err error
	for i, wait := 0, 10*time.Millisecond; ; i, wait = i+1, 2*wait {
		if b, err = os.ReadFile(path); err == nil || errors.Is(err, fs.ErrNotExist) || i == 6 {
			break
		}
		time.Sleep(wait)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrJournalUnreadable, path, err)
	}
	if j.V != journalVersion {
		return nil, fmt.Errorf("%w: %s: journal version %d, this release reads %d", ErrJournalUnreadable, path, j.V, journalVersion)
	}
	upgradeClickStates(&j)
	return &j, nil
}

// What a journal from before owner decision D1 was replaced (2026-10-27) may say: its
// order waited for a click on this machine (awaiting_click, step ask), then for the go
// (clicked).
const (
	legacyAwaitingClick = "awaiting_click"
	legacyClicked       = "clicked"
	legacyStepAsk       = "ask"
)

// upgradeClickStates reads a journal that waited for a click as one that waits for the
// app (ordered) or for the home's go (app_staged): a run left waiting goes on, with no
// click.
func upgradeClickStates(j *Journal) {
	switch j.State {
	case legacyAwaitingClick:
		j.State = StateOrdered
	case legacyClicked:
		j.State = StateAppStaged
	}
	if j.Step == legacyStepAsk {
		j.Step = StepOrder
	}
	if j.App == legacyClicked {
		j.App = RequestStaged
	}
	for i := range j.Steps {
		if j.Steps[i].Step == legacyStepAsk {
			j.Steps[i].Step = StepOrder
		}
	}
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

// Backup is a dump of the database: where it is and how large.
type Backup struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}
