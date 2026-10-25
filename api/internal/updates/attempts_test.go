package updates

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// attemptHome is the home's supervisor answering its status with attempts.
type attemptHome struct{ st *MachineStatus }

func (h attemptHome) Do(context.Context, string, any) (*MachineStatus, error) { return h.st, nil }
func (h attemptHome) Log(context.Context, string) ([]byte, error)             { return nil, nil }

// attemptCluster adds the machines' own attempts to their update.state.
type attemptCluster struct {
	*clusterFake
	attempts map[string][]Attempt
}

func (c attemptCluster) MachineStates() map[string]json.RawMessage {
	out := c.clusterFake.MachineStates()
	for id, raw := range out {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		m["attempts"] = c.attempts[id]
		out[id], _ = json.Marshal(m)
	}
	return out
}

// update-attempts.md: the overview's machines carry their last attempts, newest first:
// the home's from its supervisor's status, a node's from its update.state, each merged
// with its last finished part in a run (source run, with the run to retry).
func TestMachinesCarryTheirAttempts(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "darwin-arm64", "1.4.0")
	at := time.Date(2026, 10, 24, 5, 30, 0, 0, time.UTC)
	app := Attempt{ID: "app-1", At: at, Source: "app", Trigger: "badge", From: "1.4.0", To: "1.5.0",
		Outcome: OutcomeFailed, Step: "install", Code: "needs_admin", Detail: "Permission denied"}
	r.d.Cluster = attemptCluster{clusterFake: r.cluster, attempts: map[string][]Attempt{"n1": {app}}}
	start := Attempt{ID: "start-1.5.0-1", At: at.Add(time.Minute), Source: "server", Trigger: "start", From: "1.4.0",
		To: "1.5.0", Outcome: OutcomePutBack, Step: "migrate", Code: "db_migrate_failed", Detail: `relation "videos"`}
	r.d.Home = attemptHome{st: &MachineStatus{Attempts: []Attempt{start}}}
	ms := r.d.Machines(ctx, Machine{ID: "home", Name: "home", Role: "home", Version: "1.4.0"})
	if len(ms) != 2 || len(ms[0].Attempts) != 1 || ms[0].Attempts[0].Code != "db_migrate_failed" ||
		ms[0].Attempts[0].Outcome != OutcomePutBack {
		t.Fatalf("home %+v", ms[0].Attempts)
	}
	if len(ms[1].Attempts) != 1 || ms[1].Attempts[0].Code != "needs_admin" || ms[1].Attempts[0].Source != "app" {
		t.Fatalf("node %+v", ms[1].Attempts)
	}
}

// A node's part in a run that ended (rolled back) is an attempt of its own, with the run
// to retry and the machine's reason; a run about the home is the home's.
func TestRunPartsAreAttempts(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0").outcome = MRolledBack
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 80)
	a, ok, err := r.f.store.LastTargetAttempt(ctx, "n1")
	if err != nil || !ok || a.Source != SourceRun || a.RunID != run.ID || a.Outcome != OutcomeRolledBack ||
		a.To != "1.5.0" || a.From != "1.4.0" {
		t.Fatalf("node's run attempt %+v %v %v (run %s)", a, ok, err, run.State)
	}
	home, ok := r.f.store.homeRunAttempt(ctx)
	if !ok || home.RunID != run.ID || home.Outcome != OutcomeInstalled || home.To != "1.5.0" {
		t.Fatalf("home's run attempt %+v %v", home, ok)
	}
	ms := r.d.Machines(ctx, Machine{ID: "home", Role: "home"})
	if len(ms[1].Attempts) == 0 || ms[1].Attempts[0].RunID != run.ID {
		t.Fatalf("node attempts %+v", ms[1].Attempts)
	}
}

// The run's error document becomes the attempt's step, code and detail (a reason when
// the detail is missing).
func TestRunAttemptReadsTheError(t *testing.T) {
	t.Parallel()
	end := time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)
	a := runAttempt(Run{ID: "r1", Version: "v1.5.0", FromVersion: "1.4.0", Trigger: TriggerAuto, State: RunRolledBack,
		StartedAt: end.Add(-time.Hour), FinishedAt: &end,
		Error: json.RawMessage(`{"code": "rolled_back", "reason": "api crash loop"}`)})
	if a.Outcome != OutcomeRolledBack || a.Code != "rolled_back" || a.Detail != "api crash loop" || a.To != "1.5.0" ||
		a.EndedAt == nil || a.Trigger != TriggerAuto {
		t.Fatalf("%+v", a)
	}
	f := runAttempt(Run{ID: "r2", Version: "1.5.0", State: RunFailed, StartedAt: end,
		Error: json.RawMessage(`{"code": "disk_space", "step": "prepare", "detail": "4 GB free, 8 GB needed"}`)})
	if f.Outcome != OutcomeFailed || f.Step != "prepare" || f.Code != "disk_space" || f.Detail == "" {
		t.Fatalf("%+v", f)
	}
}

// A machine's detail reaches the page without URLs' queries and cut to maxDetail (a node of
// an older build sends them whole).
func TestMergedAttemptsAreClean(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", 2000)
	got := mergeAttempts([]Attempt{{ID: "app-1", At: time.Now(), Detail: "GET https://cdn.example/a.tar.gz?sig=s3cret: 403 " + long,
		Fix: "see https://x.example/p?token=t"}})
	if strings.Contains(got[0].Detail, "s3cret") || len(got[0].Detail) > maxDetail || !strings.HasSuffix(got[0].Detail, "…") ||
		got[0].Fix != "see https://x.example/p" {
		t.Fatalf("%d %q", len(got[0].Detail), got[0].Fix)
	}
}

// A machine's failed login to the home's database reaches the page once it saw it twice.
func TestHomeDBShown(t *testing.T) {
	t.Parallel()
	if (&HomeDB{OK: false, Code: "refused", Fails: 1}).shown() != nil {
		t.Fatal("one failed try shown")
	}
	if h := (&HomeDB{OK: false, Code: "refused", Fails: 2}).shown(); h == nil || h.Code != "refused" {
		t.Fatalf("a confirmed failure: %+v", h)
	}
	if h := (&HomeDB{OK: true}).shown(); h == nil || !h.OK {
		t.Fatal("ok")
	}
	if (*HomeDB)(nil).shown() != nil {
		t.Fatal("nil")
	}
	// the machine's request for its access and the home's answer reach the page too
	var st agentState
	if err := json.Unmarshal([]byte(`{"home_db":{"ok":false,"code":"refused","fails":5,"asked":"2026-10-25T09:02:00Z",`+
		`"answer":{"ok":false,"code":"cannot_alter_role","reason":"may not change this machine's role"}}}`), &st); err != nil {
		t.Fatal(err)
	}
	if h := st.HomeDB.shown(); h == nil || h.Asked == nil || h.Answer == nil || h.Answer.Code != "cannot_alter_role" {
		t.Fatalf("the request and its answer: %+v", h)
	}
}

// The query goes, the sentence's punctuation after the URL stays.
func TestCleanDetailKeepsThePunctuation(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"GET https://x/a.tar.gz?sig=s3cret): 403":            "GET https://x/a.tar.gz): 403",
		"see https://x/p?token=t.":                           "see https://x/p.",
		"fetch (https://x/a?X-Amz-Signature=abc&x=1) failed": "fetch (https://x/a) failed",
		"https://x/a?b=c":                                    "https://x/a",
		"no url here: 403":                                   "no url here: 403",
	} {
		if got := cleanDetail(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
