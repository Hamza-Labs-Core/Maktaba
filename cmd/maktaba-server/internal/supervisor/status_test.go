package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// a supervisor whose processes are never started: the tests set their states
func boardOf(t *testing.T, procs ...Process) (*Supervisor, *StatusBoard) {
	t.Helper()
	for i := range procs {
		procs[i].Cmd = exec.Command("true")
	}
	s := New(procs, testPolicy(), WithLog(&strings.Builder{}))
	b := NewStatusBoard(s)
	b.Now = func() time.Time { return time.Date(2026, 11, 6, 10, 15, 0, 0, time.UTC) }
	b.Log = func(string, string, string, string) {}
	return s, b
}

func setState(s *Supervisor, role Role, instance string, st State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.proc.Role == role && c.proc.Instance == instance {
			c.status.State = st
		}
	}
}

func homeProcs() []Process {
	return []Process{
		{Role: RolePostgres}, {Role: RoleAPI}, {Role: RoleStreaming}, {Role: RolePipeline},
		{Role: RolePipeline, Instance: "stt-0"}, {Role: RolePipeline, Instance: "stt-1"},
		{Role: RolePipeline, Instance: "index"},
	}
}

func allRunning(s *Supervisor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		c.status.State = StateRunning
	}
}

// T01: the groups and the overall rule.
func TestStatusGroupsAndOverall(t *testing.T) {
	s, b := boardOf(t, homeProcs()...)
	if d := b.Doc(); d.Components["api"] != StatusStarting {
		t.Fatalf("before anything ran, every group starts: %v", d.Components)
	}
	allRunning(s)
	b.Check(context.Background())
	d := b.Doc()
	if d.Overall != StatusOK || d.Banner != "" || len(d.FeaturesDown) != 0 {
		t.Fatalf("healthy: %+v", d)
	}
	for _, g := range []string{"database", "api", "streaming", "processing", "search"} {
		if d.Components[g] != StatusOK {
			t.Errorf("%s = %s", g, d.Components[g])
		}
	}
	if d.Components["logs"] != StatusOff {
		t.Errorf("no log index role yet: logs = %s", d.Components["logs"])
	}

	// the api crashes: at once, without a check round
	setState(s, RoleAPI, "", StateBackoff)
	d = b.Doc()
	if d.Components["api"] != StatusRestarting || d.Overall != StatusDown || d.Banner != "api_down" ||
		!hasWord(d.FeaturesDown, "all") {
		t.Fatalf("api restarting: %+v", d)
	}
	setState(s, RoleAPI, "", StateFailed)
	if d = b.Doc(); d.Components["api"] != StatusDown || d.Overall != StatusDown {
		t.Fatalf("api cut off: %+v", d)
	}
	setState(s, RoleAPI, "", StateRunning)
	b.Check(context.Background())

	// streaming down: playback and Live TV
	setState(s, RoleStreaming, "", StateFailed)
	d = b.Doc()
	if d.Overall != StatusDegraded || d.Banner != "streaming_down" || !hasWord(d.FeaturesDown, "playback") ||
		!hasWord(d.FeaturesDown, "live_tv") {
		t.Fatalf("streaming down: %+v", d)
	}
	setState(s, RoleStreaming, "", StateRunning)
	b.Check(context.Background())

	// one of two transcription workers down: processing degraded, not down
	setState(s, RolePipeline, "stt-1", StateFailed)
	if d = b.Doc(); d.Components["processing"] != StatusDegraded || d.Banner != "processing_down" {
		t.Fatalf("one worker down: %+v", d)
	}
}

// T02: a failing check moves a state only once two checks agree; a recovery too.
func TestStatusNeedsTwoChecks(t *testing.T) {
	s, b := boardOf(t, homeProcs()...)
	fail := false
	b.DBCheck = func(context.Context) error {
		if fail {
			return errors.New("refused")
		}
		return nil
	}
	allRunning(s)
	b.Check(context.Background())
	if d := b.Doc(); d.Components["database"] != StatusOK {
		t.Fatalf("database: %v", d.Components)
	}
	fail = true
	b.Check(context.Background())
	if d := b.Doc(); d.Components["database"] != StatusOK {
		t.Fatalf("one failed check must not move it: %v", d.Components)
	}
	b.Check(context.Background())
	if d := b.Doc(); d.Components["database"] != StatusDegraded || d.Banner != "database_down" {
		t.Fatalf("two failed checks: %+v", d)
	}
	fail = false
	b.Check(context.Background())
	if d := b.Doc(); d.Components["database"] != StatusDegraded {
		t.Fatalf("one good check must not clear it: %v", d.Components)
	}
	b.Check(context.Background())
	if d := b.Doc(); d.Components["database"] != StatusOK {
		t.Fatalf("two good checks clear it: %v", d.Components)
	}
	// an unhealthy (alive, failing its probe) api also waits for two rounds
	setState(s, RoleAPI, "", StateUnhealthy)
	if d := b.Doc(); d.Components["api"] != StatusOK {
		t.Fatal("a failing probe is not a crash: it waits for the checks")
	}
	b.Check(context.Background())
	b.Check(context.Background())
	if d := b.Doc(); d.Components["api"] != StatusDegraded {
		t.Fatalf("api degraded after two rounds: %v", d.Components)
	}
}

// T06: off is not down: turned off, held, waiting on a requirement or kept off this boot
// reads off and raises no banner; failed with crash_loop is down.
func TestStatusOffIsNotDown(t *testing.T) {
	s, b := boardOf(t, append(homeProcs(), Process{Role: RoleTVSlot, Instance: "blue"})...)
	allRunning(s)
	phases := map[string]ComponentPhase{}
	b.Phases = func() map[string]ComponentPhase { return phases }
	b.Check(context.Background())
	for _, ph := range []ComponentPhase{
		{Phase: components.PhaseOff},
		{Phase: components.PhaseStopped, Reason: "held"},
		{Phase: components.PhaseWaiting, Reason: "dependency_off"},
		{Phase: components.PhaseOff, Reason: "boot_flag"},
	} {
		phases["transcription"] = ph
		setState(s, RolePipeline, "stt-0", StateStopped)
		setState(s, RolePipeline, "stt-1", StateStopped)
		d := b.Doc()
		if d.Components["processing"] != StatusOK || d.Banner != "" || d.Overall != StatusOK {
			t.Errorf("transcription %v: %+v", ph, d)
		}
	}
	// no TV: the slots off leave streaming as core.streaming says
	phases["livetv.slots"] = ComponentPhase{Phase: components.PhaseNotInstalled}
	setState(s, RoleTVSlot, "blue", StateFailed)
	if d := b.Doc(); d.Components["streaming"] != StatusOK {
		t.Errorf("no TV: streaming = %s", d.Components["streaming"])
	}
	// a component that should run and crash-looped is down
	delete(phases, "livetv.slots")
	phases["core.streaming"] = ComponentPhase{Phase: components.PhaseFailed, Reason: "crash_loop"}
	setState(s, RoleStreaming, "", StateFailed)
	if d := b.Doc(); d.Components["streaming"] != StatusDown {
		t.Errorf("crash loop: streaming = %s", d.Components["streaming"])
	}
}

// T03: the document names no host, path or version, and only "v" is a number.
func TestStatusDocSaysNothingPrivate(t *testing.T) {
	s, b := boardOf(t, homeProcs()...)
	allRunning(s)
	setState(s, RoleStreaming, "", StateFailed)
	rec := httptest.NewRecorder()
	b.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status.json", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, cache %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	body := rec.Body.String()
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	withoutTime := strings.Replace(body, doc["at"].(string), "", 1)
	if regexp.MustCompile(`[0-9]`).MatchString(strings.Replace(withoutTime, `"v":1`, "", 1)) {
		t.Errorf("a number beyond v: %s", body)
	}
	for _, bad := range []string{s.host, "/", "127.0.0.1", "localhost", "pid"} {
		if bad != "" && strings.Contains(withoutTime, bad) {
			t.Errorf("the document names %q: %s", bad, body)
		}
	}
	rec = httptest.NewRecorder()
	b.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/status.json", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

// Each change is a status_group_changed line.
func TestStatusChangesAreLogged(t *testing.T) {
	s, b := boardOf(t, homeProcs()...)
	var got []string
	b.Log = func(g, from, to, _ string) { got = append(got, g+":"+from+">"+to) }
	allRunning(s)
	b.Check(context.Background())
	setState(s, RoleAPI, "", StateBackoff)
	b.Doc()
	if !hasWord(got, "api:starting>ok") || !hasWord(got, "api:ok>restarting") {
		t.Fatalf("changes: %v", got)
	}
}

func hasWord(xs []string, x string) bool { //nolint:unparam // a test helper
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
