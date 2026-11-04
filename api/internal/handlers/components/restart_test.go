package components

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
)

// restartFixture is the fixture behind a fake supervisor and an API runner whose one
// loop, api:enrichment_auto_accept (component enrich), counts its starts.
func restartFixture(t *testing.T) (*fixture, *fakeSupervisor, *atomic.Int32) {
	t.Helper()
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	loops := runner.New(runner.Options{Host: svc.Host, Context: ctx})
	var starts atomic.Int32
	loops.Declare("enrichment_auto_accept", runner.Func(func(ctx context.Context) {
		starts.Add(1)
		<-ctx.Done()
	}))
	restartWait(t, func() bool { return starts.Load() == 1 })
	return f, withSupervisorAndLoops(t, f, loops), &starts
}

func restartWait(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

// restartAudit is the payload of the last component.restart row for id.
func restartAudit(t *testing.T, f *fixture, id string) map[string]any {
	t.Helper()
	var raw string
	if err := f.db.QueryRow(`SELECT payload FROM audit_log WHERE category = 'components' AND action = $1
		AND target_id = $2 ORDER BY id DESC LIMIT 1`, svc.ActionRestart, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A process component restarts through the supervisor (200 with the count); one whose
// processes do not run now is 202 (the supervisor starts them). Each is one audit row,
// component.restart, with before and after (the same row: nothing changes), by and a
// batch id; the desired row and its revision do not move.
func TestRestartProcess(t *testing.T) {
	f, sup, _ := restartFixture(t)
	rev := f.revision(t)
	var v struct {
		ID        string `json:"id"`
		Restarted int    `json:"restarted"`
	}
	decode(t, f.do("POST", "/api/components/transcription/restart", "", admin()), 200, &v)
	if v.ID != "transcription" || v.Restarted != 2 {
		t.Fatalf("%+v", v)
	}
	if !slices.Contains(sup.called(), "POST /components/transcription/restart") {
		t.Fatalf("the supervisor was not asked: %v", sup.called())
	}
	decode(t, f.do("POST", "/api/components/llm/restart", "", admin()), 202, &v)
	if v.Restarted != 0 {
		t.Fatalf("llm: %+v", v)
	}
	if f.revision(t) != rev || f.actions(t, svc.ActionRestart) != 2 {
		t.Fatalf("revision %d -> %d, %d restart rows", rev, f.revision(t), f.actions(t, svc.ActionRestart))
	}
	p := restartAudit(t, f, "transcription")
	before, _ := p["before"].(map[string]any)
	after, _ := p["after"].(map[string]any)
	if before["want"] != "enabled" || after["want"] != "enabled" || p["by"] == nil || p["batch_id"] == "" || p["batch_id"] == nil {
		t.Fatalf("payload %v", p)
	}
}

// A loop component of the API is cancelled and started again by the API's runner; the
// supervisor is not asked (enrich has no process).
func TestRestartLoop(t *testing.T) {
	f, sup, starts := restartFixture(t)
	var v struct {
		Restarted int `json:"restarted"`
	}
	decode(t, f.do("POST", "/api/components/enrich/restart", "", admin()), 200, &v)
	if v.Restarted != 1 {
		t.Fatalf("%+v", v)
	}
	restartWait(t, func() bool { return starts.Load() == 2 })
	if slices.ContainsFunc(sup.called(), func(c string) bool { return c == "POST /components/enrich/restart" }) {
		t.Fatalf("the supervisor was asked for a loop: %v", sup.called())
	}
	if f.actions(t, svc.ActionRestart) != 1 {
		t.Fatalf("%d restart rows", f.actions(t, svc.ActionRestart))
	}
}

// What has nothing the API can restart is 409 not_restartable: stages, an extension, an
// asset, a loop of another service; part of the server is not_switchable; off or held
// is not_running. None calls the supervisor, starts a loop or writes a row.
func TestRestartRefused(t *testing.T) {
	f, sup, starts := restartFixture(t)
	decode(t, f.do("POST", "/api/components/transcription/stop", "", admin()), 200, nil)
	base := f.audits(t)
	for id, want := range map[string]string{
		"youtube":          svc.CodeNotRestartable, // stages
		"ext.plex":         svc.CodeNotRestartable,
		"runtime.ml":       svc.CodeNotRestartable,
		"livetv.hdhomerun": svc.CodeNotRestartable, // streaming:hdhr_ssdp
		"core.api":         svc.CodeNotSwitchable,
		"cloud.relay":      svc.CodeNotRunning, // installed, not enabled
		"transcription":    svc.CodeNotRunning, // held
	} {
		p := code(t, f.do("POST", "/api/components/"+id+"/restart", "", admin()), 409, want)
		if want != svc.CodeNotSwitchable && p["id"] != id {
			t.Errorf("%s: %v", id, p)
		}
	}
	if rec := f.do("POST", "/api/components/nope/restart", "", admin()); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
	if slices.ContainsFunc(sup.called(), func(c string) bool { return strings.HasSuffix(c, "/restart") }) {
		t.Fatalf("the supervisor was asked: %v", sup.called())
	}
	if starts.Load() != 1 || f.audits(t) != base {
		t.Fatalf("starts %d, audit rows %d -> %d", starts.Load(), base, f.audits(t))
	}
}

// The restart route is the admin's: no sign-in 401; a viewer, a play sign-in, a token
// without the admin scope and a machine 403, with no supervisor call and no row; an
// admin and a manage link pass.
func TestRestartAdminOnly(t *testing.T) {
	f, sup, _ := restartFixture(t)
	const path = "/api/components/transcription/restart"
	if rec := f.do("POST", path, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no one: %d", rec.Code)
	}
	for name, p := range map[string]*principal.Principal{"viewer": viewer(), "play": playAdmin(), "token": token(), "machine": machine()} {
		if rec := f.do("POST", path, "", p); rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if len(sup.called()) != 0 || f.audits(t) != 0 {
		t.Fatalf("refused callers: %v, %d rows", sup.called(), f.audits(t))
	}
	for name, p := range map[string]*principal.Principal{"admin": admin(), "manage": manageLink()} {
		if rec := f.do("POST", path, "", p); rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}
