package components

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store/pqlisten"
)

func pgFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	db, dsn := clustertest.ScratchDB(t)
	f := &fixture{db: db, st: svc.New(db), licence: true}
	if _, err := f.st.SetDesiredBatch(context.Background(), func(map[string]store.Desired, int64) (store.BatchPlan, error) {
		var p store.BatchPlan
		for _, id := range []string{"llm", "classify", "transcription", "runtime.ml", "livetv", "livetv.slots"} {
			p.Writes = append(p.Writes, store.BatchWrite{Desired: store.Desired{ID: id, Want: components.WantEnabled},
				Audit: store.Audit{By: store.SeedBy, ActorSource: "seed", Action: "component.seed"}})
		}
		p.Writes = append(p.Writes, store.BatchWrite{Desired: store.Desired{ID: "livetv.inprocess", Want: components.WantInstalled},
			Audit: store.Audit{By: store.SeedBy, ActorSource: "seed", Action: "component.seed"}})
		return p, nil
	}); err != nil {
		t.Fatal(err)
	}
	f.auditBase = f.audits(t) // audit_log is append-only on Postgres
	s := &svc.Service{Store: f.st, Catalog: components.Default()}
	r := chi.NewRouter()
	(&Handler{Service: s, DB: db}).Mount(r)
	f.r = r
	return f, dsn
}

// T09: a PUT on Postgres writes the row, one audit row with before and after (who: the
// admin's user name when there is one), and NOTIFY components_changed.
func TestPGPutAuditsAndNotifies(t *testing.T) {
	f, dsn := pgFixture(t)
	l, err := pqlisten.New(dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	time.Sleep(100 * time.Millisecond) // the LISTEN is in place
	for len(l.Events()) > 0 {
		<-l.Events()
	}
	gen := f.want(t, "llm").Generation
	decode(t, f.do("PUT", "/api/components/llm", `{"want":"installed"}`, admin()), 200, nil)
	if d := f.want(t, "llm"); d.Want != components.WantInstalled || d.Generation != gen+1 {
		t.Fatalf("llm %+v", d)
	}
	if f.want(t, "classify").Want != components.WantEnabled {
		t.Fatal("classify's wish moved")
	}
	var action, source, payload string
	if err := f.db.QueryRow(`SELECT action, actor_source, payload::text FROM audit_log
		WHERE category = 'components' AND target_kind = 'component' AND target_id = 'llm' ORDER BY id DESC LIMIT 1`).Scan(&action, &source, &payload); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	_ = json.Unmarshal([]byte(payload), &p)
	if action != "component.disable" || source != "web" || p["before"] == nil || p["after"] == nil || p["by"] != "user:u1" {
		t.Fatalf("audit %s %s %s", action, source, payload)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-l.Events():
			if e.ID == "llm" {
				return
			}
		case <-deadline:
			t.Fatal("no components_changed for llm")
		}
	}
}

// T10 on Postgres: a batch whose third change is refused writes nothing; a refused remove
// and a refused conflict change nothing; with_conflicts changes both in one transaction.
func TestPGApplyAllOrNone(t *testing.T) {
	f, _ := pgFixture(t)
	rev := f.revision(t)
	code(t, f.do("POST", "/api/components/apply", `{"changes":[{"id":"llm","want":"installed"},
		{"id":"classify","want":"installed"},{"id":"core.api","want":"installed"}]}`, admin()), 409, svc.CodeNotSwitchable)
	code(t, f.do("PUT", "/api/components/runtime.ml", `{"want":"absent"}`, admin()), 409, svc.CodeRequiredBy)
	code(t, f.do("PUT", "/api/components/livetv.inprocess", `{"want":"enabled"}`, admin()), 409, svc.CodeConflicts)
	if f.revision(t) != rev || f.audits(t) != 0 {
		t.Fatalf("refusals wrote: revision %d -> %d, %d audit rows", rev, f.revision(t), f.audits(t))
	}
	decode(t, f.do("PUT", "/api/components/livetv.inprocess", `{"want":"enabled","with_conflicts":true}`, admin()), 200, nil)
	if f.want(t, "livetv.slots").Want != components.WantInstalled || f.audits(t) != 2 {
		t.Fatal("with_conflicts did not change both")
	}
	// a hold is {by, at} with no end, read back the same
	decode(t, f.do("POST", "/api/components/transcription/stop", "", admin()), 200, nil)
	if h := f.want(t, "transcription").Hold; h == nil || h.By != "user:u1" || h.At.IsZero() {
		t.Fatalf("hold %+v", h)
	}
	var v svc.Detail
	decode(t, f.do("GET", "/api/components/transcription", "", admin()), 200, &v)
	if len(v.Audit) == 0 || v.Audit[0].Action != "component.hold" {
		t.Fatalf("audit %+v", v.Audit)
	}
}

// T12 on Postgres: an import by an admin (a real user) writes the desired row and the
// audit rows with the admin as actor; the offline file's rows have no component target.
func TestPGImportAndOfflineAudited(t *testing.T) {
	f, _ := pgFixture(t)
	sup := withSupervisor(t, f)
	sup.built = true
	const uid = "6f1c2a8e-3b7d-4c11-9a52-0d3e4f5a6b7c"
	if _, err := f.db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, 'maya', 'x', true)`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.SetDesired(context.Background(), store.Desired{ID: "model.whisper-medium", Want: components.WantAbsent},
		store.Audit{By: "test", Action: "component.remove"}); err != nil {
		t.Fatal(err)
	}
	maya := &principal.Principal{UserID: uid, IsAdmin: true}
	decode(t, f.doType("POST", "/api/components/import?name=home.tar", "TAR", "application/x-tar", maya), 200, nil)
	if d := f.want(t, "model.whisper-medium"); d.Want != components.WantInstalled {
		t.Fatalf("model.whisper-medium %+v", d)
	}
	var v svc.Detail
	decode(t, f.do("GET", "/api/components/model.whisper-medium", "", maya), 200, &v)
	if len(v.Audit) < 2 || v.Audit[0].Action != svc.ActionImport || v.Audit[0].ActorUserID != uid || v.Audit[0].ActorSource != "web" {
		t.Fatalf("audit %+v", v.Audit)
	}
	var p map[string]any
	_ = json.Unmarshal(v.Audit[0].Payload, &p)
	if p["by"] != "maya" || p["file"] != "home.tar" {
		t.Fatalf("payload %s", v.Audit[0].Payload)
	}
	decode(t, f.do("POST", "/api/components/offline", `{"bundle":"core"}`, maya), 202, nil)
	if rec := f.do("GET", "/api/components/offline/maktaba-offline-1.2.3-linux-amd64-core.tar", "", maya); rec.Code != 200 {
		t.Fatalf("download %d", rec.Code)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM audit_log WHERE category = 'components' AND action IN ($1, $2)
		AND target_id IS NULL AND actor_user_id = $3`, svc.ActionOfflineMake, svc.ActionOfflineDownload, uid).Scan(&n); err != nil || n != 2 {
		t.Fatalf("offline audit rows: %d %v", n, err)
	}
}
