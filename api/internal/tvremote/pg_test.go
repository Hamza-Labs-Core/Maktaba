package tvremote_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
)

// Slot 0125 on real Postgres (skips without MAKTABA_TEST_PG_URL): the
// controller's SQL on the production types, the relay's functions, and
// what a machine's login may read.

func pgDenied(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "42501"
}

// TC7: tv_push_credentials answers only the home's own user; a machine's
// login (a member of maktaba_worker) is refused, and cannot read the secret
// table, the sessions or the usage either.
func TestPushCredentialsOnlyForHome(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	ctx := context.Background()
	f := cftest.New(t)
	ch := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'One', 'one', 'shuffle')`, ch); err != nil {
		t.Fatal(err)
	}
	store := secrets.NewMemory()
	_ = store.Put(ctx, tvremote.SecretAPIToken, []byte("tok-secret"), "")
	kid := "k-pg"
	f.Keys[kid] = cf.SigningKey{ID: kid, PEM: signingPEM()}
	_ = store.Put(ctx, tvremote.SecretSigningKey, []byte(`{"id":"`+kid+`","pem":"`+signingPEM()+`"}`), "")
	in := &tvremote.Integration{DB: db, Secrets: store, NewClient: f.Client,
		Ext: &fakeExt{settings: map[string]any{"account_id": "acc1", "customer_code": "abc123xyz"}}}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES ('tv_remote.enabled', 'true', now())`); err != nil {
		t.Fatal(err)
	}
	c := &tvremote.Controller{DB: db, Integration: in, TuneWait: -1}
	r, err := c.Tune(ctx, viewer("u1"), ch, "phone")
	if err != nil || r.State != "starting" {
		t.Fatalf("tune %+v %v", r, err)
	}
	var push string
	if err := db.QueryRow(`SELECT id FROM tv_remote_pushes WHERE channel_id = $1`, ch).Scan(&push); err != nil {
		t.Fatal(err)
	}
	// the home (table owner): credentials, then its report moves the push to connecting
	var url, key string
	var sid sql.NullString
	var ingest string
	if err := db.QueryRow(`SELECT url, key, stream_id, ingest FROM tv_push_credentials($1)`, push).Scan(&url, &key, &sid, &ingest); err != nil {
		t.Fatalf("home credentials: %v", err)
	}
	if url == "" || key == "" || ingest != "rtmps" {
		t.Fatalf("credentials %q %q %q", url, key, ingest)
	}
	var ok bool
	if err := db.QueryRow(`SELECT tv_push_report($1, 'connecting', 'ffmpeg up')`, push).Scan(&ok); err != nil || !ok {
		t.Fatalf("report: %v", err)
	}
	var state string
	_ = db.QueryRow(`SELECT state FROM tv_remote_pushes WHERE id = $1`, push).Scan(&state)
	if state != tvremote.PushConnecting {
		t.Fatalf("state %s", state)
	}
	if err := db.QueryRow(`SELECT tv_push_report($1, 'bogus', '')`, push).Scan(&ok); err == nil {
		t.Error("an unknown relay state was accepted")
	}

	// Cloudflare goes live; the controller's SQL on timestamptz/numeric/date
	var input string
	_ = db.QueryRow(`SELECT i.cf_uid FROM tv_remote_pushes p JOIN tv_remote_inputs i ON i.id = p.input_id WHERE p.id = $1`, push).Scan(&input)
	f.GoLive(input)
	if err := c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = c.Tune(ctx, viewer("u1"), ch, "phone")
	if err != nil || r.State != "live" {
		t.Fatalf("live tune %+v %v", r, err)
	}
	if _, err := c.Heartbeat(ctx, viewer("u1"), r.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := c.PollUsage(ctx, nil, tvremote.DefaultSettings(), time.Now()); err != nil {
		t.Fatalf("usage on Postgres: %v", err)
	}
	if _, err := c.Status(ctx); err != nil {
		t.Fatalf("status on Postgres: %v", err)
	}

	// a machine's login
	if _, err := db.Exec(`DROP ROLE IF EXISTS e32_node_test`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE ROLE e32_node_test LOGIN PASSWORD 'nodepw' IN ROLE maktaba_worker`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP ROLE IF EXISTS e32_node_test`) })
	node, err := sql.Open("postgres", clustertest.WithUser(t, dsn, "e32_node_test", "nodepw"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })
	var x any
	if err := node.QueryRow(`SELECT url FROM tv_push_credentials($1)`, push).Scan(&x); !pgDenied(err) {
		t.Errorf("a machine read push credentials: %v", err)
	}
	if err := node.QueryRow(`SELECT tv_push_report($1, 'stopped', '')`, push).Scan(&x); !pgDenied(err) {
		t.Errorf("a machine reported on a push: %v", err)
	}
	for _, q := range []string{`SELECT key FROM tv_push_secrets LIMIT 1`, `SELECT count(*) FROM tv_remote_sessions`,
		`SELECT count(*) FROM tv_remote_usage_daily`, `UPDATE tv_remote_pushes SET state = 'done' RETURNING id`} {
		if err := node.QueryRow(q).Scan(&x); !pgDenied(err) {
			t.Errorf("%s as a machine: %v, want permission denied", q, err)
		}
	}
	// goose's history: readable (the node schema guard), never writable
	if err := node.QueryRow(`SELECT max(version_id) FROM goose_db_version`).Scan(&x); err != nil {
		t.Errorf("a machine reads goose_db_version: %v", err)
	}
	if err := node.QueryRow(`DELETE FROM goose_db_version WHERE version_id = -1 RETURNING id`).Scan(&x); !pgDenied(err) {
		t.Errorf("a machine may write goose_db_version: %v", err)
	}
	// what it may read: the pool's and pushes' public columns
	var n int
	if err := node.QueryRow(`SELECT count(*) FROM tv_remote_pushes`).Scan(&n); err != nil || n != 1 {
		t.Errorf("a machine reads pushes: %d %v", n, err)
	}
	if err := node.QueryRow(`SELECT count(*) FROM tv_remote_inputs`).Scan(&n); err != nil || n != 1 {
		t.Errorf("a machine reads inputs: %d %v", n, err)
	}
	// one push per channel on Postgres (the partial unique index)
	if _, err := db.Exec(`INSERT INTO tv_remote_pushes (channel_id, state) VALUES ($1, 'requested')`, ch); err == nil {
		t.Error("a second running push of the channel was accepted")
	}
}
