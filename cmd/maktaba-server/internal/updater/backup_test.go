package updater

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// sideRehearser takes the home's pre-update dump while the release serves (SideBackup).
type sideRehearser struct {
	*fakeRehearser
	pending int
	known   bool
	err     error
	// hold, when set, keeps the dump going until it is closed
	hold chan struct{}

	mu      sync.Mutex
	backups []string // from>to of every dump taken
	asked   []string // the migrations folders asked about
}

func (s *sideRehearser) Pending(_ context.Context, dir string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, dir)
	return s.pending, s.known
}

func (s *sideRehearser) BackupBefore(ctx context.Context, from, to string, progress func(int64)) (*Backup, error) {
	s.mu.Lock()
	s.backups = append(s.backups, from+">"+to)
	s.mu.Unlock()
	progress(5 << 20)
	if s.hold != nil {
		select {
		case <-s.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return &Backup{Path: "/backups/pre-" + from + "-to-" + to + ".dump", Bytes: 3 << 30}, nil
}

func (s *sideRehearser) taken() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.backups...)
}

// nextRecords is fakeRecords that notes the dump taken for the next release (NextRecorder).
type nextRecords struct {
	fakeRecords
	mu    *sync.Mutex
	noted *[]string
}

func (r nextRecords) NoteNext(running, next, dump string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.noted = append(*r.noted, running+">"+next+":"+dump)
	return nil
}

func sideRig(t *testing.T, s *sideRehearser) (*rig, *[]string) {
	t.Helper()
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	s.fakeRehearser = &fakeRehearser{}
	r.m.Rehearser = s
	noted := &[]string{}
	r.m.Records = nextRecords{fakeRecords: fakeRecords{w}, mu: &sync.Mutex{}, noted: noted}
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
	r.waitState(StatePrepared)
	return r, noted
}

// A release that adds no migration takes no backup: the apply goes straight to the switch
// and the restart.
func TestApplyWithoutMigrationsTakesNoBackup(t *testing.T) {
	s := &sideRehearser{pending: 0, known: true}
	r, noted := sideRig(t, s)
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	r.waitExit()
	j := r.waitState(StateRestarting)
	if len(s.taken()) != 0 || len(*noted) != 0 || j.Backup != nil {
		t.Fatalf("backups %v noted %v journal %+v", s.taken(), *noted, j.Backup)
	}
	st := stepOf(j, StepBackup)
	if st == nil || st.State != "skipped" || !strings.Contains(st.Detail, "adds no migration") {
		t.Fatalf("the backup step: %+v", st)
	}
	if s.asked[0] != "/fake/migrations" {
		t.Fatalf("asked about %v, want the staged release's migrations", s.asked)
	}
	if !r.w.called(StepSwitch) {
		t.Fatal("not switched")
	}
}

// A release that adds migrations: the running release dumps while it goes on serving
// (nothing switched, no restart, the step saying how much it wrote), and only then
// switches; the dump is the run's backup, noted for the new release's start.
func TestApplyWithMigrationsBacksUpWhileServing(t *testing.T) {
	s := &sideRehearser{pending: 2, known: true, hold: make(chan struct{})}
	r, noted := sideRig(t, s)
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		j := r.m.Status().Journal
		if st := stepOf(j, StepBackup); st != nil && st.Detail == "5 MB written" {
			if j.State != StateApplying {
				t.Fatalf("state %s while the dump runs", j.State)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no progress said: %+v", r.m.Status().Steps)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if r.w.called(StepSwitch) || len(r.exited) != 0 {
		t.Fatal("switched or restarted before the dump was done")
	}
	close(s.hold)
	r.waitExit()
	j := r.waitState(StateRestarting)
	if got := s.taken(); len(got) != 1 || got[0] != "1.4.0>1.5.0" {
		t.Fatalf("backups %v", got)
	}
	if len(*noted) != 1 || (*noted)[0] != "1.4.0>1.5.0:/backups/pre-1.4.0-to-1.5.0.dump" {
		t.Fatalf("noted %v", *noted)
	}
	if j.Backup == nil || j.Backup.Bytes != 3<<30 {
		t.Fatalf("the run's backup: %+v", j.Backup)
	}
	if st := stepOf(j, StepBackup); st == nil || st.State != "done" || !strings.HasPrefix(st.Detail, "3072 MB in ") {
		t.Fatalf("the backup step: %+v", st)
	}
	// the step comes before the switch
	var order []string
	for _, st := range j.Steps {
		order = append(order, st.Step)
	}
	if !strings.Contains(strings.Join(order, " "), "backup switch restart") {
		t.Fatalf("steps %v", order)
	}
}

// Whether the release adds migrations cannot be told: it is backed up all the same.
func TestApplyUnknownMigrationsBacksUp(t *testing.T) {
	s := &sideRehearser{known: false}
	r, _ := sideRig(t, s)
	_ = r.m.Apply("run-1")
	r.waitExit()
	r.waitState(StateRestarting)
	if len(s.taken()) != 1 {
		t.Fatalf("backups %v", s.taken())
	}
}

// A backup that fails switches nothing: the release before goes on, the run says why.
func TestApplyBackupFailureSwitchesNothing(t *testing.T) {
	s := &sideRehearser{pending: 1, known: true, err: errors.New("not enough free space for the database dump")}
	r, noted := sideRig(t, s)
	_ = r.m.Apply("run-1")
	j := r.waitState(StateApplyFailed)
	if j.Error == nil || j.Error.Step != StepBackup || j.Error.Code != "db_dump_failed" {
		t.Fatalf("error %+v", j.Error)
	}
	r.m.Wait()
	if r.w.called(StepSwitch) || len(r.exited) != 0 || r.w.next != "1.4.0" || len(*noted) != 0 {
		t.Fatalf("switched %v exits %d next %s noted %v", r.w.called(StepSwitch), len(r.exited), r.w.next, *noted)
	}
}

// Rolling back a release that added no migration restarts on the release before at once:
// nothing is dumped and nothing is put back.
func TestRollbackWithoutMigrationsIsARestart(t *testing.T) {
	s := &sideRehearser{pending: 0, known: true}
	r, _ := sideRig(t, s)
	_ = r.m.Apply("run-1")
	r.waitExit()
	n := r.restart(Verdict{Fatal: true, Why: "failed_tv"})
	n.waitExit()
	n.waitState(StateRollingBack)
	if n.w.marked || len(s.taken()) != 0 || n.w.next != "1.4.0" {
		t.Fatalf("marked %v backups %v next %s", n.w.marked, s.taken(), n.w.next)
	}
}

// Rolling back a release whose migrations ran marks the dump taken before them to be put
// back by the release before's start, and dumps nothing on the way.
func TestRollbackAfterMigrationsMarksTheBackupOnly(t *testing.T) {
	s := &sideRehearser{pending: 3, known: true}
	r, _ := sideRig(t, s)
	_ = r.m.Apply("run-1")
	r.waitExit()
	r.w.migrated = true
	n := r.restart(Verdict{Fatal: true, Why: "api crash loop"})
	n.waitExit()
	n.waitState(StateRollingBack)
	if !n.w.marked || len(s.taken()) != 1 || n.w.next != "1.4.0" {
		t.Fatalf("marked %v backups %v (the one before the switch) next %s", n.w.marked, s.taken(), n.w.next)
	}
}
