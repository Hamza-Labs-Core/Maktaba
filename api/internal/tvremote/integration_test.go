package tvremote_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver ("sqlite")

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
)

// fakeExt is the extensions table for the integration.
type fakeExt struct {
	mu       sync.Mutex
	settings map[string]any
	enabled  bool
	cleared  bool
}

func (e *fakeExt) ExtensionSettings(context.Context, string) (map[string]any, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]any{}
	for k, v := range e.settings {
		out[k] = v
	}
	return out, e.enabled, nil
}

func (e *fakeExt) SaveExtensionSetting(_ context.Context, _, key, value string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.settings[key] = value
	return nil
}

func (e *fakeExt) ClearExtension(context.Context, string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.settings, e.enabled, e.cleared = map[string]any{}, false, true
	return nil
}

type fixture struct {
	f     *cftest.Server
	in    *tvremote.Integration
	ext   *fakeExt
	store *secrets.Memory
	db    *sql.DB
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []string{
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT, updated_by TEXT)`,
		`CREATE TABLE home_secrets (name TEXT PRIMARY KEY, ciphertext BLOB, nonce BLOB, key_id TEXT, updated_at TEXT, updated_by TEXT)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range upStatements(t, filepath.Join(migrationsDir(), "0125_tv_remote.sqlite.sql")) {
		if _, err := db.Exec(strings.ReplaceAll(s, "ADD COLUMN IF NOT EXISTS", "ADD COLUMN")); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	fx := &fixture{f: cftest.New(t), db: db, store: secrets.NewMemory(),
		ext: &fakeExt{settings: map[string]any{"account_id": "acc1"}}, now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	_ = fx.store.Put(context.Background(), tvremote.SecretAPIToken, []byte("tok-secret"), "")
	fx.in = &tvremote.Integration{
		DB: db, Secrets: fx.store, Ext: fx.ext,
		NewClient: fx.f.Client,
		Now:       func() time.Time { return fx.now },
		CanPush: func(context.Context) (tvremote.PushAbility, error) {
			return tvremote.PushAbility{RTMPS: true, Source: "test"}, nil
		},
	}
	return fx
}

func lines(r tvremote.TestResult) map[string]tvremote.Line {
	out := map[string]tvremote.Line{}
	for _, l := range r.Lines {
		out[l.Key] = l
	}
	return out
}

// TC3: each failing check is reported; the signing key is created once.
func TestCFTestConnection(t *testing.T) {
	ctx := context.Background()

	t.Run("all green, key created once, customer code learned", func(t *testing.T) {
		fx := newFixture(t)
		fx.f.AddVideo("someinput", "ready")
		r, err := fx.in.Test(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !r.OK {
			t.Fatalf("not ok: %+v", r.Lines)
		}
		ls := lines(r)
		for _, k := range []string{"token", "stream", "storage", "analytics", "signing_key", "customer_domain", "push"} {
			if !ls[k].OK {
				t.Errorf("%s: %+v", k, ls[k])
			}
		}
		if fx.ext.settings["customer_code"] != "abc123xyz" {
			t.Errorf("customer code not saved: %v", fx.ext.settings)
		}
		k, err := fx.in.SigningKey(ctx)
		if err != nil || k.ID == "" || k.PEM == "" {
			t.Fatalf("signing key stored: %+v %v", k, err)
		}
		if _, err := fx.in.Test(ctx); err != nil {
			t.Fatal(err)
		}
		if fx.f.KeyPost != 1 {
			t.Errorf("signing key created %d times, want once", fx.f.KeyPost)
		}
		for _, req := range fx.f.Requests {
			if strings.Contains(req, "tok-secret") {
				t.Errorf("token in a URL: %s", req)
			}
		}
	})

	t.Run("key deleted at Cloudflare is made again", func(t *testing.T) {
		fx := newFixture(t)
		fx.ext.settings["customer_code"] = "abc123xyz"
		_, _ = fx.in.Test(ctx)
		fx.f.Keys = map[string]cf.SigningKey{}
		_, _ = fx.in.Test(ctx)
		if fx.f.KeyPost != 2 {
			t.Errorf("KeyPost = %d", fx.f.KeyPost)
		}
	})

	t.Run("lost secrets.key: re-enter the token (EC3)", func(t *testing.T) {
		fx := newFixture(t)
		fx.store.Corrupt(tvremote.SecretAPIToken)
		r, _ := fx.in.Test(ctx)
		if r.OK || !strings.Contains(lines(r)["token"].Detail, "enter the API token again") {
			t.Fatalf("token line %+v", lines(r)["token"])
		}
		if !lines(r)["stream"].Skipped {
			t.Error("later checks must be skipped")
		}
		st, _ := fx.in.Status(ctx)
		if st.TokenProblem == nil || st.Configured {
			t.Errorf("status %+v", st)
		}
	})

	t.Run("no account id", func(t *testing.T) {
		fx := newFixture(t)
		fx.ext.settings = map[string]any{}
		r, _ := fx.in.Test(ctx)
		if r.OK || lines(r)["token"].Detail != "no account ID saved" {
			t.Fatalf("%+v", r.Lines)
		}
	})
}

// TC3 (continued): each failing check is reported (split from TestCFTestConnection so
// no single test runs past the unit tier's per-test cap under -race).
func TestCFTestConnectionFailingChecks(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		set    func(f *cftest.Server)
		failed string
		ok     bool
		detail string
	}{
		{"invalid token", func(f *cftest.Server) { f.Token = "other" }, "token", false, "1000 Invalid API Token"},
		{"disabled token", func(f *cftest.Server) { f.TokenStatus = "disabled" }, "token", false, "the token is disabled"},
		{"no stream", func(f *cftest.Server) { f.NoStream = true }, "stream", false, "no Stream subscription"},
		{"no storage plan", func(f *cftest.Server) { f.StorageLimit = 0 }, "storage", false, "no storage capacity"},
		{"no analytics is only a warning", func(f *cftest.Server) { f.AnalyticsDenied = true }, "analytics", true, "session minutes only"},
		{"no stream write", func(f *cftest.Server) { f.KeysDenied = true }, "signing_key", false, "needs Stream Write"},
		{"no playback domain anywhere", func(*cftest.Server) {}, "customer_domain", false, "enter the customer subdomain code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t)
			if tc.failed != "customer_domain" {
				fx.ext.settings["customer_code"] = "abc123xyz"
			}
			tc.set(fx.f)
			r, err := fx.in.Test(ctx)
			if err != nil {
				t.Fatal(err)
			}
			l := lines(r)[tc.failed]
			if l.OK || !strings.Contains(l.Detail, tc.detail) {
				t.Errorf("%s line = %+v", tc.failed, l)
			}
			if r.OK != tc.ok {
				t.Errorf("overall ok = %v, want %v (%s)", r.OK, tc.ok, r.Message)
			}
		})
	}

}

// TC3 (continued): the pool input and the push check.
func TestCFTestConnectionPoolAndPush(t *testing.T) {
	ctx := context.Background()
	t.Run("pool input created when the account has none", func(t *testing.T) {
		fx := newFixture(t)
		_, _ = fx.in.Test(ctx)
		if len(fx.f.Inputs) != 1 {
			t.Fatalf("inputs %v", fx.f.Inputs)
		}
		for _, in := range fx.f.Inputs {
			if in.Name() != tvremote.PoolInputName || in.Enabled == nil || *in.Enabled || !in.Recording.RequireSignedURLs {
				t.Errorf("pool input %+v", in)
			}
		}
		_, _ = fx.in.Test(ctx)
		if len(fx.f.Inputs) != 1 {
			t.Error("the pool input must be reused, not created again")
		}
	})

	t.Run("home cannot push", func(t *testing.T) {
		fx := newFixture(t)
		fx.ext.settings["customer_code"] = "abc123xyz"
		fx.in.CanPush = func(context.Context) (tvremote.PushAbility, error) {
			return tvremote.PushAbility{Source: "ffmpeg"}, nil
		}
		r, _ := fx.in.Test(ctx)
		if r.OK || lines(r)["push"].OK {
			t.Errorf("%+v", lines(r)["push"])
		}
	})
}

// TC4: remote can only be switched on after a green test < 24 h old.
func TestRemoteEnableNeedsGreenTest(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	fx.ext.settings["customer_code"] = "abc123xyz"
	if err := fx.in.SetEnabled(ctx, true); !errors.Is(err, tvremote.ErrNeedsGreenTest) {
		t.Fatalf("no test yet: %v", err)
	}
	fx.f.NoStream = true
	if r, _ := fx.in.Test(ctx); r.OK {
		t.Fatal("expected a failed test")
	}
	if err := fx.in.SetEnabled(ctx, true); !errors.Is(err, tvremote.ErrNeedsGreenTest) {
		t.Fatalf("failed test: %v", err)
	}
	fx.f.NoStream = false
	if r, _ := fx.in.Test(ctx); !r.OK {
		t.Fatalf("green test expected: %+v", r.Lines)
	}
	if err := fx.in.SetEnabled(ctx, true); err != nil {
		t.Fatalf("green test: %v", err)
	}
	st, _ := fx.in.Status(ctx)
	if !st.Enabled || st.LastTest == nil || !st.LastTest.OK || !st.LastTest.CheckedAt.Equal(fx.now) {
		t.Fatalf("status %+v", st)
	}
	fx.now = fx.now.Add(25 * time.Hour)
	if err := fx.in.SetEnabled(ctx, true); !errors.Is(err, tvremote.ErrNeedsGreenTest) {
		t.Errorf("stale test: %v", err)
	}
	if err := fx.in.SetEnabled(ctx, false); err != nil {
		t.Errorf("off never needs a test: %v", err)
	}
}

// AC5: removing deletes Maktaba's inputs and the key at Cloudflare, the
// secrets and the settings, and turns remote off.
func TestRemoveIntegration(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	fx.ext.settings["customer_code"] = "abc123xyz"
	cl := fx.f.Client("acc1", "tok-secret")
	theirs, _ := cl.CreateLiveInput(ctx, cf.CreateLiveInput{Meta: map[string]any{"name": "someone-else"}})
	if r, _ := fx.in.Test(ctx); !r.OK {
		t.Fatalf("%+v", r.Lines)
	}
	mine, _ := cl.CreateLiveInput(ctx, cf.CreateLiveInput{Meta: map[string]any{"name": "maktaba-pool-2"}})
	if err := fx.in.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	// a broadcast that was just stopped left its recording at Cloudflare
	rec := fx.f.AddVideo(mine.UID, "ready")
	theirRec := fx.f.AddVideo(theirs.UID, "ready")
	stopped, forgot := false, false
	fx.in.StopPushes = func(context.Context) error { stopped = true; return nil }
	fx.in.ForgetInputs = func(context.Context) error { forgot = true; return nil }
	res, err := fx.in.Remove(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !stopped || len(res.Errors) != 0 || len(res.DeletedInputs) != 1 || res.DeletedInputs[0] != mine.UID || res.DeletedSigningKey == "" {
		t.Fatalf("remove = %+v", res)
	}
	if fx.f.Inputs[theirs.UID] == nil || fx.f.Inputs[mine.UID] != nil || len(fx.f.Keys) != 0 {
		t.Errorf("cloudflare after remove: inputs %v keys %v", fx.f.Inputs, fx.f.Keys)
	}
	if fx.f.Videos[rec.UID] != nil || fx.f.Videos[theirRec.UID] == nil || len(res.DeletedRecordings) != 1 {
		t.Errorf("recordings after remove: %v (deleted %v)", fx.f.Videos, res.DeletedRecordings)
	}
	if !forgot {
		t.Error("the local input rows were not forgotten")
	}
	for _, n := range []string{tvremote.SecretAPIToken, tvremote.SecretSigningKey} {
		if ok, _ := fx.store.Has(ctx, n); ok {
			t.Errorf("%s not deleted", n)
		}
	}
	st, _ := fx.in.Status(ctx)
	if st.Enabled || st.LastTest != nil || !fx.ext.cleared {
		t.Errorf("status after remove %+v", st)
	}
}

// EC2: the account id cannot change while Maktaba has a key on it.
func TestAccountChangeRefusedWhileInUse(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	cur := map[string]any{"account_id": "acc1"}
	if err := fx.in.ValidateSettings(ctx, cur, map[string]any{"account_id": "acc2"}, nil); err != nil {
		t.Fatalf("nothing on the account yet: %v", err)
	}
	// live inputs on the account (no signing key yet) also hold the account
	if _, err := fx.db.Exec(`INSERT INTO tv_remote_inputs (id, cf_uid, name) VALUES ('i1', 'cfin1', 'maktaba-x-1')`); err != nil {
		t.Fatal(err)
	}
	if err := fx.in.ValidateSettings(ctx, cur, map[string]any{"account_id": "acc2"}, nil); err == nil {
		t.Fatal("account change allowed while Maktaba has live inputs on it")
	}
	_, _ = fx.db.Exec(`DELETE FROM tv_remote_inputs`)
	fx.ext.settings["customer_code"] = "abc123xyz"
	_, _ = fx.in.Test(ctx)
	err := fx.in.ValidateSettings(ctx, cur, map[string]any{"account_id": "acc2"}, nil)
	var he *httperror.Error
	if !errors.As(err, &he) || he.Status != http.StatusConflict || !strings.Contains(he.Detail, "remove the integration first") {
		t.Fatalf("change while in use: %v", err)
	}
	if err := fx.in.ValidateSettings(ctx, cur, map[string]any{"account_id": "acc1"}, []string{"api_token"}); err != nil {
		t.Errorf("token replacement (EC1) must be allowed: %v", err)
	}
}

func TestParseProtocols(t *testing.T) {
	out := "Supported file protocols:\nInput:\n  rtmps\n  srt\nOutput:\n  file\n  rtmp\n  rtmps\n  tcp\n"
	ab := tvremote.ParseProtocols(out)
	if !ab.RTMPS || ab.SRT {
		t.Errorf("ab = %+v (srt is input-only here)", ab)
	}
}

// Post-review: a pasted domain still names the right playback host, and a
// value that is no customer code is refused on save and never used.
func TestCustomerCodeNormalized(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	fx.ext.settings["customer_code"] = "customer-abc123xyz.cloudflarestream.com"
	st, err := fx.in.Status(ctx)
	if err != nil || st.CustomerDomain == nil || *st.CustomerDomain != "customer-abc123xyz.cloudflarestream.com" {
		t.Fatalf("pasted domain: %+v %v", st.CustomerDomain, err)
	}
	fx.ext.settings["customer_code"] = "x@evil.example/"
	if st, _ := fx.in.Status(ctx); st.CustomerDomain != nil {
		t.Fatalf("a non-code was used: %q", *st.CustomerDomain)
	}
	err = fx.in.ValidateSettings(ctx, map[string]any{}, map[string]any{"customer_code": "x@evil.example/"}, nil)
	var he *httperror.Error
	if !errors.As(err, &he) || he.Status != http.StatusBadRequest {
		t.Fatalf("bad code saved: %v", err)
	}
	if err := fx.in.ValidateSettings(ctx, map[string]any{}, map[string]any{"customer_code": "abc123xyz"}, nil); err != nil {
		t.Fatalf("a code refused: %v", err)
	}
}
