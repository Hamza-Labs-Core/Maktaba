package switchover

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

type fakeSecrets map[string]string

func (f fakeSecrets) Get(_ context.Context, name string) ([]byte, error) {
	if v, ok := f[name]; ok {
		return []byte(v), nil
	}
	return nil, errors.New("no such secret")
}

// The services of the home's network are checked from the new home: one that answers
// there moved; one at the old home's own address moves to the old home's network address
// when it answers there (the pipeline's setting changed); one that answers nowhere needs
// a person; one not used is not.
func TestTheHomesServicesAnswerFromTheNewHome(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	plex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/identity" || r.Header.Get("X-Plex-Token") != "tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"p"}}`))
	}))
	defer plex.Close()
	pu, _ := url.Parse(plex.URL)
	_, port, _ := net.SplitHostPort(pu.Host)
	store := fakeSecrets{"ext.plex.token": "tok"}
	set := func(ext, addr string, enabled bool) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO extensions (name, enabled, settings, updated_at)
		    VALUES ($1, $2, jsonb_build_object('url', $3::text), now())
		    ON CONFLICT (name) DO UPDATE SET enabled = EXCLUDED.enabled, settings = EXCLUDED.settings`, ext, enabled, addr); err != nil {
			t.Fatal(err)
		}
	}
	// it answers at its address
	set("plex", plex.URL, true)
	if got := checkService(ctx, db, store, plexService, ""); got.State != Moved || got.Detail != plex.URL {
		t.Fatalf("Plex at its address: %+v", got)
	}
	// its token does not open here
	if got := checkService(ctx, db, fakeSecrets{}, plexService, ""); got.State != Failed {
		t.Fatalf("Plex without its token: %+v", got)
	}
	// it was at the old home's own address: moved to the old home's network address
	set("plex", "http://127.0.0.2:"+port, true)
	got := checkService(ctx, db, store, plexService, "127.0.0.1")
	if got.State != Moved || got.Detail != "http://127.0.0.1:"+port {
		t.Fatalf("Plex at the old home's own address: %+v", got)
	}
	var now string
	_ = db.QueryRowContext(ctx, `SELECT settings->>'url' FROM extensions WHERE name = 'plex'`).Scan(&now)
	if now != "http://127.0.0.1:"+port {
		t.Fatalf("the pipeline's Plex address: %q", now)
	}
	// it answers nowhere: a person says where it is
	set("plex", "http://127.0.0.2:1", true)
	if got := checkService(ctx, db, store, plexService, "127.0.0.3"); got.State != NeedsYou || got.Detail != "http://127.0.0.2:1" {
		t.Fatalf("Plex nowhere: %+v", got)
	}
	// no address here (the old home had it from its environment)
	set("plex", "", true)
	t.Setenv("MAKTABA_PLEX_URL", "")
	if got := checkService(ctx, db, store, plexService, ""); got.State != NeedsYou || got.Detail != "no_address" {
		t.Fatalf("Plex without an address: %+v", got)
	}
	// dizqueTV off, then answering
	set("dizquetv", "http://127.0.0.2:1", false)
	if got := checkService(ctx, db, store, dizquetvService, ""); got.State != NotUsed {
		t.Fatalf("dizqueTV off: %+v", got)
	}
	dz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/channels" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer dz.Close()
	set("dizquetv", dz.URL, true)
	if got := checkService(ctx, db, store, dizquetvService, ""); got.State != Moved {
		t.Fatalf("dizqueTV: %+v", got)
	}
}

// Cloudflare's notifications: not used without their secret; until one reaches this home
// since the switch, the path to set at its public address; then moved. Through the cloud
// relay, which moved with the switch, they are moved at once.
func TestCloudflaresNotificationsMoveWithTheHome(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	noRelay := Integration{State: NotUsed}
	if got := cfWebhook(ctx, db, noRelay); got.State != NotUsed {
		t.Fatalf("no webhook: %+v", got)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO home_secrets (name, key_id, nonce, ciphertext)
	    VALUES ('cf_webhook_secret', 'k1', '\x01'::bytea, '\x02'::bytea)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cluster_terms (term, home_name, reason) VALUES (2, 'b', 'planned')`); err != nil {
		t.Fatal(err)
	}
	got := cfWebhook(ctx, db, noRelay)
	if got.State != NeedsYou || got.Detail != "/api/tv/remote/webhook" {
		t.Fatalf("not reached yet: %+v", got)
	}
	if got := cfWebhook(ctx, db, Integration{State: Moved, Detail: "library"}); got.State != Moved ||
		!strings.Contains(got.Detail, "cloud relay (library)") {
		t.Fatalf("through the relay: %+v", got)
	}
	if got := cfWebhook(ctx, db, Integration{State: Pending}); got.State != NeedsYou {
		t.Fatalf("a relay not online yet: %+v", got)
	}
	// an event from before the switch does not count
	if _, err := db.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at)
	    VALUES ($1, to_jsonb(now() - interval '1 day'), now())`, WebhookSeen); err != nil {
		t.Fatal(err)
	}
	if got := cfWebhook(ctx, db, noRelay); got.State != NeedsYou {
		t.Fatalf("an old event: %+v", got)
	}
	if _, err := db.ExecContext(ctx, `UPDATE app_settings SET value = to_jsonb(now() + interval '1 second') WHERE key = $1`,
		WebhookSeen); err != nil {
		t.Fatal(err)
	}
	if got := cfWebhook(ctx, db, noRelay); got.State != Moved {
		t.Fatalf("reached: %+v", got)
	}
}

// No model runtime on the new home is not used while the library has no answer of a
// model's, and needs a person once it has (the old home ran one).
func TestAModelRuntimeIsNeededOnlyWhereTheLibraryUsedOne(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	t.Setenv("MAKTABA_OLLAMA_URL", "http://127.0.0.1:1")
	if got := llm(ctx, db); got.State != NotUsed {
		t.Fatalf("a library without a model's answers: %+v", got)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO display_title_llm (input_hash, model, title) VALUES ('h', 'qwen', 't')`); err != nil {
		t.Fatal(err)
	}
	if got := llm(ctx, db); got.State != NeedsYou {
		t.Fatalf("a library that used one: %+v", got)
	}
}
