package tvremote_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
)

// The controller tests run on the SQLite twin of slot 0125 (the store's SQL is
// portable) with the fake Cloudflare API and a fake clock. The home's push
// relay is simulated by writing what tv_push_report writes.

var (
	keyOnce sync.Once
	keyPEM  string
)

// signingPEM is one RSA key for the whole package (key generation is slow
// under -race).
func signingPEM() string {
	keyOnce.Do(func() {
		k, _ := rsa.GenerateKey(rand.Reader, 2048)
		p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
		keyPEM = base64.StdEncoding.EncodeToString(p)
	})
	return keyPEM
}

func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "db", "migrations")
}

// upStatements are the Up section's statements of a goose file.
func upStatements(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	up, _, _ := strings.Cut(text, "-- +goose Down")
	_, up, _ = strings.Cut(up, "-- +goose Up")
	var out []string
	for _, blk := range strings.Split(up, "-- +goose StatementBegin") {
		blk, _, _ = strings.Cut(blk, "-- +goose StatementEnd")
		for _, st := range strings.Split(blk, ";") {
			lines := []string{}
			for _, l := range strings.Split(st, "\n") {
				if s := strings.TrimSpace(l); s != "" && !strings.HasPrefix(s, "--") {
					lines = append(lines, l)
				}
			}
			if len(lines) > 0 {
				out = append(out, strings.Join(lines, "\n"))
			}
		}
	}
	return out
}

type remoteFx struct {
	t     *testing.T
	f     *cftest.Server
	db    *sql.DB
	ext   *fakeExt
	store *secrets.Memory
	in    *tvremote.Integration
	c     *tvremote.Controller
	now   time.Time
	mu    sync.Mutex
	kid   string
	chans []string
}

func (fx *remoteFx) clock() time.Time {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return fx.now
}

func (fx *remoteFx) advance(d time.Duration) {
	fx.mu.Lock()
	fx.now = fx.now.Add(d)
	fx.mu.Unlock()
}

func newRemote(t *testing.T) *remoteFx {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []string{
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT, updated_by TEXT)`,
		`CREATE TABLE channels (id TEXT PRIMARY KEY, library_id TEXT, number INTEGER NOT NULL, name TEXT NOT NULL,
			slug TEXT NOT NULL, mode TEXT NOT NULL DEFAULT 'shuffle', enabled INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE channel_placements (channel_id TEXT PRIMARY KEY, host_id TEXT NOT NULL, generation INTEGER NOT NULL,
			state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT 'first_request')`,
		`CREATE TABLE instance_identity (id INTEGER PRIMARY KEY, instance_id TEXT NOT NULL, name TEXT)`,
		`INSERT INTO instance_identity (id, instance_id, name) VALUES (1, '1a2b3c4d-0000-4000-8000-000000000000', 'home')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range upStatements(t, filepath.Join(migrationsDir(), "0125_tv_remote.sqlite.sql")) {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	fx := &remoteFx{t: t, f: cftest.New(t), db: db, store: secrets.NewMemory(),
		ext: &fakeExt{settings: map[string]any{"account_id": "acc1", "customer_code": "abc123xyz"}},
		now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	fx.f.Now = fx.clock
	fx.kid = "key-" + uuid.NewString()[:8]
	fx.f.Keys[fx.kid] = cf.SigningKey{ID: fx.kid, PEM: signingPEM()}
	blob, _ := json.Marshal(map[string]string{"id": fx.kid, "pem": signingPEM()})
	_ = fx.store.Put(context.Background(), tvremote.SecretAPIToken, []byte("tok-secret"), "")
	_ = fx.store.Put(context.Background(), tvremote.SecretSigningKey, blob, "")
	fx.in = &tvremote.Integration{DB: db, Secrets: fx.store, Ext: fx.ext, NewClient: fx.f.Client, Now: fx.clock}
	fx.c = &tvremote.Controller{DB: db, Integration: fx.in, Now: fx.clock, TuneWait: -1}
	fx.setting(tvremote.SettingEnabled, true)
	for i := 0; i < 6; i++ {
		id := uuid.NewString()
		fx.exec(`INSERT INTO channels (id, number, name, slug) VALUES ($1, $2, $3, $4)`, id, 101+i, "Ch", "ch"+id[:4])
		fx.chans = append(fx.chans, id)
	}
	return fx
}

func (fx *remoteFx) exec(q string, args ...any) {
	fx.t.Helper()
	if _, err := fx.db.Exec(q, args...); err != nil {
		fx.t.Fatalf("%s: %v", q, err)
	}
}

func (fx *remoteFx) setting(key string, v any) {
	fx.t.Helper()
	b, _ := json.Marshal(v)
	fx.exec(`INSERT INTO app_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, string(b))
}

// rules sets rules.tv_remote.
func (fx *remoteFx) rules(m map[string]any) { fx.setting(tvremote.SettingsKey, m) }

func viewer(id string) tvremote.Viewer {
	return tvremote.Viewer{UserID: id, CanRead: func(*string) bool { return true }}
}

func (fx *remoteFx) tune(user, ch, device string) (tvremote.Tuned, *tvremote.TuneError) {
	fx.t.Helper()
	r, err := fx.c.Tune(context.Background(), viewer(user), ch, device)
	if err != nil {
		var te *tvremote.TuneError
		if errors.As(err, &te) {
			return r, te
		}
		fx.t.Fatalf("tune: %v", err)
	}
	return r, nil
}

func (fx *remoteFx) reconcile() {
	fx.t.Helper()
	if err := fx.c.Reconcile(context.Background()); err != nil {
		fx.t.Fatal(err)
	}
}

type pushRow struct {
	ID, State, Video, Input, Reason string
	Attempt                         int
}

func (fx *remoteFx) pushes(ch string) []pushRow {
	fx.t.Helper()
	rows, err := fx.db.Query(`SELECT id, state, COALESCE(video_uid, ''), COALESCE(input_id, ''), COALESCE(end_reason, ''), attempt
		FROM tv_remote_pushes WHERE channel_id = $1 ORDER BY started_at, attempt`, ch)
	if err != nil {
		fx.t.Fatal(err)
	}
	defer rows.Close()
	var out []pushRow
	for rows.Next() {
		var p pushRow
		if err := rows.Scan(&p.ID, &p.State, &p.Video, &p.Input, &p.Reason, &p.Attempt); err != nil {
			fx.t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func (fx *remoteFx) last(ch string) pushRow {
	fx.t.Helper()
	ps := fx.pushes(ch)
	if len(ps) == 0 {
		fx.t.Fatalf("no push for %s", ch)
	}
	return ps[len(ps)-1]
}

// relay simulates the home's push relay reporting (tv_push_report).
func (fx *remoteFx) relay(push, state string) {
	fx.t.Helper()
	at := fx.clock().Format(time.RFC3339Nano)
	fx.exec(`UPDATE tv_remote_pushes SET relay_state = $1, relay_at = $2,
		state = CASE WHEN $1 = 'connecting' AND state = 'requested' THEN 'connecting' ELSE state END,
		state_at = CASE WHEN $1 = 'connecting' AND state = 'requested' THEN $2 ELSE state_at END WHERE id = $3`, state, at, push)
}

func (fx *remoteFx) cfInput(push string) string {
	fx.t.Helper()
	var uid string
	if err := fx.db.QueryRow(`SELECT i.cf_uid FROM tv_remote_pushes p JOIN tv_remote_inputs i ON i.id = p.input_id WHERE p.id = $1`, push).Scan(&uid); err != nil {
		fx.t.Fatal(err)
	}
	return uid
}

// goLive takes a requested push to live: relay connecting, Cloudflare live, a reconcile.
func (fx *remoteFx) goLive(ch string) pushRow {
	fx.t.Helper()
	p := fx.last(ch)
	fx.relay(p.ID, "connecting")
	fx.f.GoLive(fx.cfInput(p.ID))
	fx.reconcile()
	p = fx.last(ch)
	if p.State != tvremote.PushLive || p.Video == "" {
		fx.t.Fatalf("push not live: %+v", p)
	}
	return p
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
