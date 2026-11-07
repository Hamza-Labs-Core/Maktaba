package plugins

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/statements"
)

// Story 54.22 / 54.24: a third-party token never reaches a named statement, whatever it
// was granted (README §15.4: the trusted set is the release's own plugins').
func TestTrustedRoutesRefuseAThirdPartyToken(t *testing.T) {
	f := newFixture(t)
	m := f.mint(t)
	for _, route := range []string{"trusted/stmt", "trusted/tx"} {
		code, c, _ := f.call(t, m.Token, route, map[string]any{"name": "rules.read", "op": "fetch"})
		if code != 403 || c != CodeCapability {
			t.Fatalf("%s: %d %s", route, code, c)
		}
	}
}

// A job of a first-party stage gets a trusted token from the embedded bundle (no grants
// row), its statements run under the plugin's role, and a forged scope is refused.
func TestAFirstPartyJobRunsItsNamedStatements(t *testing.T) {
	raw, err := os.ReadFile("firstparty/testdata/toy.json")
	if err != nil {
		t.Fatal(err)
	}
	defer firstparty.UseBundle(raw)()
	f := newFixture(t)
	role := pq.QuoteIdentifier(statements.Role("tv.maktaba.toy"))
	f.exec(t, `DO $$ BEGIN CREATE ROLE `+role+` NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$`)
	f.exec(t, `GRANT USAGE ON SCHEMA public TO `+role)
	f.exec(t, `GRANT SELECT ON videos, app_settings TO `+role)
	f.exec(t, `CREATE TABLE fp_toy_out (library_id uuid, video_id uuid, title text, n bigint)`)
	f.exec(t, `INSERT INTO job_stages (stage, label, subject_kinds, pool, lane) VALUES ('fp_toy','Toy','{library}','cpu','seasons')`)
	var job int64
	if err := f.db.QueryRow(`INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, library_id)
		VALUES ('fp_toy','running','library',$1,$1) RETURNING id`, f.libA).Scan(&job); err != nil {
		t.Fatal(err)
	}
	m, err := f.s.Tokens.Mint(context.Background(), job)
	if err != nil || m.PluginID != "tv.maktaba.toy" || !m.AllLibraries {
		t.Fatalf("minted %+v %v", m, err)
	}
	code, c, body := f.call(t, m.Token, "trusted/stmt", map[string]any{"name": "stage.run", "op": "fetch",
		"args": []any{map[string]string{"$t": "uuid", "v": f.libA}}})
	if code != 200 {
		t.Fatalf("its own statement: %d %s %s", code, c, body)
	}
	var out struct {
		Rows [][]any `json:"rows"`
	}
	_ = json.Unmarshal(body, &out)
	if len(out.Rows) != 2 {
		t.Fatalf("library A's videos: %s", body)
	}
	code, c, _ = f.call(t, m.Token, "trusted/stmt", map[string]any{"name": "stage.run", "op": "fetch", "args": []any{f.libB}})
	if code != 403 || c != CodeScope {
		t.Fatalf("another library: %d %s", code, c)
	}
	code, c, _ = f.call(t, m.Token, "trusted/stmt", map[string]any{"name": "rules.read", "op": "fetch", "args": []any{"rules.planner"}})
	if code != 403 || c != CodeScope {
		t.Fatalf("a settings section it does not list: %d %s", code, c)
	}
	code, c, _ = f.call(t, m.Token, "trusted/stmt", map[string]any{"name": "rules.read", "op": "fetch", "args": []any{"rules.groups"}})
	if code != 200 {
		t.Fatalf("its own settings section: %d %s", code, c)
	}
	code, c, _ = f.call(t, m.Token, "trusted/stmt", map[string]any{"name": "nope", "op": "fetch"})
	if code != 403 || c != CodeCapability {
		t.Fatalf("an unregistered statement: %d %s", code, c)
	}
	// a write to a table outside its role is Postgres's refusal, answered 403
	code, c, _ = f.call(t, m.Token, "trusted/stmt", map[string]any{"name": "stage.run.2", "op": "execute",
		"args": []any{f.libA}})
	if code != 403 || c != CodeCapability {
		t.Fatalf("fp_toy_out is not granted here: %d %s", code, c)
	}
	// the fingerprint runner's token reads only
	fpm, err := f.s.Tokens.MintFingerprints(context.Background(), "tv.maktaba.toy")
	if err != nil {
		t.Fatal(err)
	}
	code, c, _ = f.call(t, fpm.Token, "trusted/stmt", map[string]any{"name": "stage.run.2", "op": "execute", "args": []any{f.libA}})
	if code != 403 || c != CodeCapability {
		t.Fatalf("a write with the fingerprint token: %d %s", code, c)
	}
}
