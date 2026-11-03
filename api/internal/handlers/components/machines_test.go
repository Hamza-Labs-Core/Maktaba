package components

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.12: per-machine overrides on the SQLite twin (T01-T05, T09, T12 as unit
// tests; the Postgres half is machines_pg_test.go).

const nodeN = "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7"

// machineFixture is newFixture with a machine N (approved, 4 GB, linux arm64, heard just
// now), its stage switches and the stage registry of every catalog stage.
func machineFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	for _, q := range []string{
		`ALTER TABLE cluster_nodes ADD COLUMN state TEXT NOT NULL DEFAULT 'approved'`,
		`ALTER TABLE cluster_nodes ADD COLUMN capabilities TEXT`,
		`ALTER TABLE cluster_nodes ADD COLUMN last_seen_at TEXT`,
		`CREATE TABLE cluster_node_stages (node_id TEXT NOT NULL, stage TEXT NOT NULL, enabled INTEGER NOT NULL,
			PRIMARY KEY (node_id, stage))`,
	} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	for _, c := range components.Default().Components() {
		for _, st := range c.Stages {
			if _, err := f.db.Exec(`INSERT INTO job_stages (stage, label, subject_kinds, pool, lane) VALUES ($1, $1, '[]', 'cpu', 'media') ON CONFLICT DO NOTHING`, st); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.addNode(t, nodeN, "old-laptop", "approved", time.Now())
	return f
}

func (f *fixture) addNode(t *testing.T, id, name, state string, seen time.Time) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO cluster_nodes (id, name, state, capabilities, last_seen_at) VALUES ($1, $2, $3, $4, $5)`,
		id, name, state, `{"os":"linux","arch":"aarch64","memory_gb":4.0,"gpus":[]}`, seen.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) switches(t *testing.T, node string) map[string]bool {
	t.Helper()
	rows, err := f.db.Query(`SELECT stage, enabled FROM cluster_node_stages WHERE node_id = $1`, node)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var s string
		var on bool
		if err := rows.Scan(&s, &on); err != nil {
			t.Fatal(err)
		}
		out[s] = on
	}
	return out
}

func (f *fixture) override(t *testing.T, node, id string) (store.Override, bool) {
	t.Helper()
	ov, err := f.st.Overrides(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := ov[id]
	return o, ok
}

func (f *fixture) machineList(t *testing.T, node string) map[string]svc.MachineComponent {
	t.Helper()
	var l svc.MachineList
	decode(t, f.do("GET", "/api/components?machine="+node, "", admin()), 200, &l)
	out := map[string]svc.MachineComponent{}
	for _, c := range l.Components {
		out[c.ID] = c
	}
	return out
}

func machinePath(id string) string { return "/api/components/" + id + "/machines/" + nodeN }

var machineRoutes = []struct{ method, path, body string }{
	{"GET", "/api/components?machine=" + nodeN, ""},
	{"PUT", machinePath("transcription"), `{"want":"installed"}`},
	{"PUT", machinePath("transcription"), `{"want":null}`},
	{"POST", machinePath("youtube") + "/stop", ""},
	{"POST", machinePath("youtube") + "/start", ""},
	{"POST", "/api/components/preview", `{"changes":[{"id":"transcription","want":"installed","machine":"` + nodeN + `"}]}`},
	{"POST", "/api/components/apply", `{"changes":[{"id":"transcription","want":"installed","machine":"` + nodeN + `"}]}`},
}

// T05: every route of §2 as no one is 401; as a viewer, an admin's cloud play sign-in, a
// token without the admin scope and a machine's certificate 403; nothing changes. An
// admin and an admin's manage link pass.
func TestMachineRoutesAdminOnly(t *testing.T) {
	f := machineFixture(t)
	rev := f.revision(t)
	for _, c := range machineRoutes {
		if rec := f.do(c.method, c.path, c.body, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s as no one: %d", c.method, c.path, rec.Code)
		}
		for name, p := range map[string]*principal.Principal{"viewer": viewer(), "play": playAdmin(), "token": token(), "machine": machine()} {
			if rec := f.do(c.method, c.path, c.body, p); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d", c.method, c.path, name, rec.Code)
			}
		}
	}
	if _, has := f.override(t, nodeN, "transcription"); has || f.revision(t) != rev || f.audits(t) != 0 || len(f.switches(t, nodeN)) != 0 {
		t.Fatal("a refused call changed something")
	}
	for name, p := range map[string]*principal.Principal{"admin": admin(), "manage": manageLink()} {
		f := machineFixture(t)
		for _, c := range machineRoutes {
			if rec := f.do(c.method, c.path, c.body, p); rec.Code/100 != 2 {
				t.Errorf("%s %s as %s: %d %s", c.method, c.path, name, rec.Code, rec.Body)
			}
		}
	}
}

// T01: "Off on this machine" writes the override (installed) and both stage switches
// (enabled = false) in one call, with one audit row naming the machine; the home keeps
// transcribing.
func TestMachineOffWritesTheStageSwitches(t *testing.T) {
	f := machineFixture(t)
	var mc svc.MachineComponent
	decode(t, f.do("PUT", machinePath("transcription"), `{"want":"installed"}`, admin()), 200, &mc)
	if mc.Here.Want != components.WantInstalled || mc.Here.Override == nil || mc.Here.Phase != components.PhaseOff {
		t.Fatalf("answered %+v", mc.Here)
	}
	if mc.Want != components.WantEnabled || f.want(t, "transcription").Want != components.WantEnabled {
		t.Fatal("the home's row moved")
	}
	if o, _ := f.override(t, nodeN, "transcription"); o.Want != components.WantInstalled {
		t.Fatalf("override %+v", o)
	}
	if sw := f.switches(t, nodeN); len(sw) != 3 || sw["extract"] || sw["transcribe"] || sw["second_listen"] {
		t.Fatalf("stage switches %v", sw)
	}
	var action, payload string
	if err := f.db.QueryRow(`SELECT action, payload FROM audit_log WHERE category = 'components' ORDER BY id DESC LIMIT 1`).
		Scan(&action, &payload); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	_ = json.Unmarshal([]byte(payload), &p)
	if f.audits(t) != 1 || action != "component.override" || p["node_id"] != nodeN || p["after"] == nil {
		t.Fatalf("audit %s %s (%d rows)", action, payload, f.audits(t))
	}
	// the same again writes nothing
	decode(t, f.do("PUT", machinePath("transcription"), `{"want":"installed"}`, admin()), 200, nil)
	if f.audits(t) != 1 {
		t.Fatal("a change that changes nothing wrote")
	}
	// the home's own view is the home's
	home := f.machineList(t, nodeN)["transcription"]
	if home.Phase == components.PhaseOff {
		t.Fatalf("the home's phase is the machine's: %+v", home.View)
	}
}

// T02: clearing the override (want null) follows the home: the stage switches go, the
// row stays with no want (its generation only moves on).
func TestMachineFollowClearsTheStageSwitches(t *testing.T) {
	f := machineFixture(t)
	decode(t, f.do("PUT", machinePath("transcription"), `{"want":"installed"}`, admin()), 200, nil)
	o1, _ := f.override(t, nodeN, "transcription")
	var mc svc.MachineComponent
	decode(t, f.do("PUT", machinePath("transcription"), `{"want":null}`, admin()), 200, &mc)
	o2, has := f.override(t, nodeN, "transcription")
	if !has || o2.Want != "" || o2.Generation <= o1.Generation {
		t.Fatalf("override %+v (was %+v)", o2, o1)
	}
	if sw := f.switches(t, nodeN); len(sw) != 0 {
		t.Fatalf("stage switches %v", sw)
	}
	if mc.Here.Want != components.WantEnabled || mc.Here.Phase != components.PhaseRunning {
		t.Fatalf("answered %+v", mc.Here)
	}
	// a want of any other shape is refused
	code(t, f.do("PUT", machinePath("transcription"), `{"want":3}`, admin()), 400, svc.CodeBadWant)
	code(t, f.do("PUT", machinePath("transcription"), `{}`, admin()), 400, svc.CodeBadWant)
}

// T03: a placement-home component cannot be turned on on a machine; core.* has no
// override; a machine there is none of is 404.
func TestMachineRefusals(t *testing.T) {
	f := machineFixture(t)
	code(t, f.do("PUT", machinePath("llm"), `{"want":"enabled"}`, admin()), 409, svc.CodeHomeOnly)
	// turning a home-only one off on a machine narrows: allowed
	decode(t, f.do("PUT", machinePath("llm"), `{"want":"installed"}`, admin()), 200, nil)
	code(t, f.do("PUT", machinePath("core.pipeline"), `{"want":"installed"}`, admin()), 409, svc.CodeNotSwitchable)
	code(t, f.do("PUT", "/api/components/nope/machines/"+nodeN, `{"want":"installed"}`, admin()), 404, svc.CodeUnknown)
	code(t, f.do("PUT", "/api/components/llm/machines/00000000-0000-4000-8000-000000000009", `{"want":"installed"}`, admin()), 404, svc.CodeUnknownMachine)
	code(t, f.do("PUT", "/api/components/llm/machines/not-a-uuid", `{"want":"installed"}`, admin()), 404, svc.CodeUnknownMachine)
	code(t, f.do("GET", "/api/components?machine=00000000-0000-4000-8000-000000000009", "", admin()), 404, svc.CodeUnknownMachine)
	f.addNode(t, "00000000-0000-4000-8000-00000000000a", "gone", "revoked", time.Now())
	code(t, f.do("PUT", "/api/components/llm/machines/00000000-0000-4000-8000-00000000000a", `{"want":"installed"}`, admin()), 404, svc.CodeUnknownMachine)
	// one request is for one machine; a bundle is the home's
	code(t, f.do("POST", "/api/components/apply", `{"changes":[{"id":"llm","want":"installed","machine":"`+nodeN+`"},{"id":"youtube","want":"installed"}]}`, admin()), 400, svc.CodeMixedMachines)
	// stale generation of the override row
	o, _ := f.override(t, nodeN, "llm")
	code(t, f.do("PUT", machinePath("llm"), `{"want":null,"generation":`+itoa(o.Generation+5)+`}`, admin()), 409, svc.CodeStale)
	decode(t, f.do("PUT", machinePath("llm"), `{"want":null,"generation":`+itoa(o.Generation)+`}`, admin()), 200, nil)
	// remove on one machine is refused while what is on there requires it
	body := code(t, f.do("PUT", machinePath("runtime.ml"), `{"want":"absent"}`, admin()), 409, svc.CodeRequiredBy)
	if by, _ := body["required_by"].([]any); !slices.Contains(by, any("transcription")) {
		t.Fatalf("required_by %v", body["required_by"])
	}
}

// T04: a placement-any component the home has off may be turned on for one machine; it
// waits there while its requirement is missing.
func TestMachineMayTurnOnWhatTheHomeHasOff(t *testing.T) {
	f := machineFixture(t)
	decode(t, f.do("PUT", "/api/components/youtube", `{"want":"installed"}`, admin()), 200, nil)
	decode(t, f.do("PUT", "/api/components/tool.yt-dlp", `{"want":"absent"}`, admin()), 200, nil)
	var mc svc.MachineComponent
	decode(t, f.do("PUT", machinePath("youtube"), `{"want":"enabled"}`, admin()), 200, &mc)
	if mc.Here.Want != components.WantEnabled || mc.Here.Phase != components.PhaseWaiting ||
		mc.Here.Reason != "dependency_off:tool.yt-dlp" || mc.Want != components.WantInstalled {
		t.Fatalf("youtube on the machine %+v (home %s)", mc.Here, mc.Want)
	}
	if sw := f.switches(t, nodeN); len(sw) != 0 {
		t.Fatalf("turning on wrote stage switches %v", sw)
	}
}

// A hold on one machine lasts until start (no end time); its stages are switched off
// there while it holds.
func TestMachineStopAndStart(t *testing.T) {
	f := machineFixture(t)
	code(t, f.do("POST", machinePath("youtube")+"/stop", `{"until":"2026-11-04T06:00:00Z"}`, admin()), 400, svc.CodeNoUntil)
	if _, has := f.override(t, nodeN, "youtube"); has {
		t.Fatal("a refused stop wrote")
	}
	var mc svc.MachineComponent
	decode(t, f.do("POST", machinePath("youtube")+"/stop", ``, admin()), 200, &mc)
	if mc.Here.Hold == nil || mc.Here.Phase != components.PhaseStopped || mc.Hold != nil {
		t.Fatalf("stopped %+v (home hold %v)", mc.Here, mc.Hold)
	}
	sw := f.switches(t, nodeN)
	c, _ := components.Default().Get("youtube")
	for _, st := range c.Stages {
		if on, has := sw[st]; !has || on {
			t.Fatalf("stage %s while held: %v", st, sw)
		}
	}
	decode(t, f.do("POST", machinePath("youtube")+"/start", ``, admin()), 200, &mc)
	if mc.Here.Hold != nil || mc.Here.Phase != components.PhaseRunning || len(f.switches(t, nodeN)) != 0 {
		t.Fatalf("started %+v, switches %v", mc.Here, f.switches(t, nodeN))
	}
	// a hold on the home stops it on the machine too: both must be cleared
	decode(t, f.do("POST", "/api/components/youtube/stop", ``, admin()), 200, nil)
	if got := f.machineList(t, nodeN)["youtube"]; got.Here.Phase != components.PhaseStopped {
		t.Fatalf("home hold on the machine %+v", got.Here)
	}
}

// The preview walks the machine's graph and changes nothing; apply writes it.
func TestMachinePreviewAndApply(t *testing.T) {
	f := machineFixture(t)
	rev := f.revision(t)
	var p svc.Preview
	decode(t, f.do("POST", "/api/components/preview",
		`{"changes":[{"id":"transcription","want":"installed","machine":"`+nodeN+`"}]}`, admin()), 200, &p)
	found := map[string]svc.Effect{}
	for _, e := range p.Effects {
		found[e.ID] = e
	}
	if found["transcription"].To != components.PhaseOff || found["subtitles"].To != components.PhaseWaiting || p.DrainS != 120 {
		t.Fatalf("effects %+v drain %d", p.Effects, p.DrainS)
	}
	if f.revision(t) != rev || len(f.switches(t, nodeN)) != 0 {
		t.Fatal("a preview wrote")
	}
	var res svc.Result
	decode(t, f.do("POST", "/api/components/apply",
		`{"changes":[{"id":"transcription","want":"installed","machine":"`+nodeN+`"},{"id":"youtube","hold":true,"machine":"`+nodeN+`"}]}`, admin()), 200, &res)
	if len(res.Overrides) != 2 || res.BatchID == "" || len(res.Changed) != 0 {
		t.Fatalf("result %+v", res)
	}
	if f.want(t, "transcription").Want != components.WantEnabled {
		t.Fatal("apply on a machine moved the home's row")
	}
}

// T09: a worker machine's choice at approve: every offered component left out becomes an
// override (installed, its stages switched off); none for what stays ticked.
func TestWorkerChoice(t *testing.T) {
	f := machineFixture(t)
	pending := "11111111-2222-4333-8444-555555555555"
	f.addNode(t, pending, "gpu-box", "pending", time.Now())
	s := &svc.Service{Store: f.st, Catalog: components.Default()}
	var offered []string
	home, _, _ := f.st.Desired(context.Background(), store.Home)
	for _, c := range components.Default().Components() {
		if svc.WorkerOffer(c, home) {
			offered = append(offered, c.ID)
		}
	}
	if !slices.Contains(offered, "youtube") || !slices.Contains(offered, "transcription") || slices.Contains(offered, "llm") ||
		slices.Contains(offered, "cluster") || slices.Contains(offered, "media.thumbnails") {
		t.Fatalf("offered %v", offered)
	}
	if err := s.WorkerChoice(context.Background(), pending, []string{"nope"}, svc.Actor{By: "maya", Source: "web"}); err == nil {
		t.Fatal("an unknown component was taken")
	}
	keep := slices.DeleteFunc(slices.Clone(offered), func(id string) bool { return id == "youtube" })
	if err := s.WorkerChoice(context.Background(), pending, keep, svc.Actor{By: "maya", Source: "web"}); err != nil {
		t.Fatal(err)
	}
	ov, _ := f.st.Overrides(context.Background(), pending)
	if len(ov) != 1 || ov["youtube"].Want != components.WantInstalled {
		t.Fatalf("overrides %+v", ov)
	}
	sw := f.switches(t, pending)
	if sw["youtube_fetch"] || len(sw) == 0 {
		t.Fatalf("youtube's stages on the machine %v", sw)
	}
	// every offered one kept: nothing written
	other := "11111111-2222-4333-8444-666666666666"
	f.addNode(t, other, "spare", "pending", time.Now())
	if err := s.WorkerChoice(context.Background(), other, offered, svc.Actor{By: "maya", Source: "web"}); err != nil {
		t.Fatal(err)
	}
	if ov, _ := f.st.Overrides(context.Background(), other); len(ov) != 0 {
		t.Fatalf("a full choice wrote %+v", ov)
	}
}

// An admin's stage switch turned back on (the Machines page's stage table) makes an
// override that kept the component off follow the home: "On, 2 stages off", naming the
// stage still off.
func TestStageSwitchOnFollowsTheHome(t *testing.T) {
	f := machineFixture(t)
	decode(t, f.do("PUT", machinePath("transcription"), `{"want":"installed"}`, admin()), 200, nil)
	// what PUT /api/cluster/nodes/{id} does: the stage row, then the hook
	if _, err := f.db.Exec(`UPDATE cluster_node_stages SET enabled = 1 WHERE node_id = $1 AND stage = 'transcribe'`, nodeN); err != nil {
		t.Fatal(err)
	}
	on := true
	s := &svc.Service{Store: f.st, Catalog: components.Default()}
	if err := s.StagesSwitched(context.Background(), nodeN, map[string]*bool{"transcribe": &on}, svc.Actor{By: "maya", Source: "web"}); err != nil {
		t.Fatal(err)
	}
	tr := f.machineList(t, nodeN)["transcription"]
	if tr.Here.Want != components.WantEnabled || !slices.Equal(tr.Here.StagesOff, []string{"extract", "second_listen"}) {
		t.Fatalf("transcription here %+v", tr.Here)
	}
	// turning a stage off changes no override
	off := false
	before := f.audits(t)
	if err := s.StagesSwitched(context.Background(), nodeN, map[string]*bool{"extract": &off}, svc.Actor{By: "maya"}); err != nil {
		t.Fatal(err)
	}
	if f.audits(t) != before {
		t.Fatal("a stage turned off wrote an override")
	}
	// "Follow home" from the component's menu rewrites its stages: it wins
	decode(t, f.do("PUT", machinePath("transcription"), `{"want":null}`, admin()), 200, nil)
	if sw := f.switches(t, nodeN); len(sw) != 0 {
		t.Fatalf("follow left stage switches %v", sw)
	}
}

// T12 (SQLite half): a machine silent for longer than 90 s shows its last status, stale;
// what it cannot run says why.
func TestMachineListStaleAndFit(t *testing.T) {
	f := machineFixture(t)
	if err := f.st.ReportStatus(context.Background(), nodeN, "pipeline", []store.Status{{ID: "transcription", Phase: components.PhaseRunning}}); err != nil {
		t.Fatal(err)
	}
	l := f.machineList(t, nodeN)
	tr := l["transcription"]
	if !tr.Here.Reported || tr.Here.Stale || tr.Here.CannotRun != "not_enough_memory" || tr.Here.RAMMB != 6000 || !tr.Here.WorkerOffer {
		t.Fatalf("fresh %+v", tr.Here)
	}
	old := time.Now().Add(-3 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := f.db.Exec(`UPDATE cluster_nodes SET last_seen_at = $1 WHERE id = $2`, old, nodeN); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE component_status SET updated_at = $1 WHERE machine = $2`, old, nodeN); err != nil {
		t.Fatal(err)
	}
	var ml svc.MachineList
	decode(t, f.do("GET", "/api/components?machine="+nodeN, "", admin()), 200, &ml)
	if !ml.Machine.Stale || ml.Machine.Platform != "linux-arm64" || ml.Machine.MemoryMB != 4096 || ml.WorkerOffered == 0 {
		t.Fatalf("machine %+v", ml.Machine)
	}
	for _, c := range ml.Components {
		if c.ID == "transcription" && (!c.Here.Stale || c.Here.Phase != components.PhaseRunning) {
			t.Fatalf("stale %+v", c.Here)
		}
	}
}
