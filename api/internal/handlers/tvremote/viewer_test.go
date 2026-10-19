package tvremote

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	remote "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
)

const chID = "6f1c9a2e-0000-4000-8000-000000000101"

func remoteServer(t *testing.T) (http.Handler, *cftest.Server, *sql.DB, *remote.Controller) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT, updated_by TEXT)`,
		`CREATE TABLE channels (id TEXT PRIMARY KEY, library_id TEXT, number INTEGER, name TEXT, slug TEXT, enabled INTEGER DEFAULT 1)`,
		`CREATE TABLE channel_placements (channel_id TEXT PRIMARY KEY, host_id TEXT, generation INTEGER, state TEXT)`,
		`INSERT INTO channels (id, number, name, slug) VALUES ('` + chID + `', 101, 'One', 'one')`,
		`INSERT INTO app_settings (key, value) VALUES ('tv_remote.enabled', 'true')`,
	}
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "db", "migrations", "0125_tv_remote.sqlite.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(b), "-- +goose Down")
	for _, blk := range strings.Split(up, "-- +goose StatementBegin") {
		blk, _, _ = strings.Cut(blk, "-- +goose StatementEnd")
		for _, s := range strings.Split(blk, ";") {
			var keep []string
			for _, l := range strings.Split(s, "\n") {
				if x := strings.TrimSpace(l); x != "" && !strings.HasPrefix(x, "--") {
					keep = append(keep, l)
				}
			}
			if len(keep) > 0 {
				stmts = append(stmts, strings.Join(keep, "\n"))
			}
		}
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v: %s", err, s)
		}
	}
	f := cftest.New(t)
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemB64 := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
	f.Keys["k1"] = cf.SigningKey{ID: "k1", PEM: pemB64}
	store := secrets.NewMemory()
	_ = store.Put(context.Background(), remote.SecretAPIToken, []byte("tok-secret"), "")
	blob, _ := json.Marshal(map[string]string{"id": "k1", "pem": pemB64})
	_ = store.Put(context.Background(), remote.SecretSigningKey, blob, "")
	in := &remote.Integration{DB: db, Secrets: store, Ext: &ext{s: map[string]any{"account_id": "acc1", "customer_code": "abc123xyz"}},
		NewClient: f.Client}
	ctl := &remote.Controller{DB: db, Integration: in, TuneWait: -1}
	r := chi.NewRouter()
	(&Handler{Integration: in, Remote: ctl}).Mount(r)
	return r, f, db, ctl
}

func as(h http.Handler, method, path, body string, p *principal.Principal, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestViewerRoutes(t *testing.T) {
	h, f, db, ctl := remoteServer(t)
	u := &principal.Principal{UserID: "u1"}
	if rec := as(h, "POST", "/api/tv/remote/tune", `{"channel_id":"`+chID+`"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous tune: %d", rec.Code)
	}
	rec := as(h, "POST", "/api/tv/remote/tune", `{"channel_id":"`+chID+`","device":"phone"}`, u)
	if rec.Code != http.StatusAccepted || rec.Header().Get("Retry-After") != "3" {
		t.Fatalf("tune: %d %s", rec.Code, rec.Body)
	}
	var started remote.Tuned
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	// the relay reports, Cloudflare goes live
	var push, input string
	_ = db.QueryRow(`SELECT p.id, i.cf_uid FROM tv_remote_pushes p JOIN tv_remote_inputs i ON i.id = p.input_id`).Scan(&push, &input)
	if _, err := db.Exec(`UPDATE tv_remote_pushes SET state = 'connecting', relay_state = 'connecting'`); err != nil {
		t.Fatal(err)
	}
	f.GoLive(input)
	if err := ctl.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec = as(h, "POST", "/api/tv/remote/tune", `{"channel_id":"`+chID+`","device":"phone"}`, u)
	var tuned remote.Tuned
	_ = json.Unmarshal(rec.Body.Bytes(), &tuned)
	if rec.Code != 200 || tuned.SessionID != started.SessionID || !strings.Contains(tuned.HLSURL, "/manifest/video.m3u8") {
		t.Fatalf("tune live: %d %s", rec.Code, rec.Body)
	}
	if rec := as(h, "POST", "/api/tv/remote/sessions/"+tuned.SessionID+"/heartbeat", ``, u); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"state":"live"`) {
		t.Fatalf("heartbeat: %d %s", rec.Code, rec.Body)
	}
	if rec := as(h, "POST", "/api/tv/remote/sessions/"+tuned.SessionID+"/heartbeat", ``, &principal.Principal{UserID: "u2"}); rec.Code != 404 {
		t.Fatalf("someone else's heartbeat: %d", rec.Code)
	}
	// refusals carry their code
	_, _ = db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tv_remote', '{"viewers":"admins"}')`)
	rec = as(h, "POST", "/api/tv/remote/tune", `{"channel_id":"`+chID+`","device":"tv"}`, &principal.Principal{UserID: "u3"})
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), `"code":"remote_not_allowed"`) {
		t.Fatalf("not allowed: %d %s", rec.Code, rec.Body)
	}
	if rec := as(h, "DELETE", "/api/tv/remote/sessions/"+tuned.SessionID, ``, u); rec.Code != 204 {
		t.Fatalf("end: %d", rec.Code)
	}
	// the admin page shows the pool and the limits
	rec = as(h, "GET", "/api/tv/remote", ``, &principal.Principal{UserID: "a", IsAdmin: true})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"channels_line":"1 of 22 remote channels in use"`) ||
		!strings.Contains(rec.Body.String(), `"stored_line":"stored minutes:`) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "key-") {
		t.Fatalf("a stream key in the status: %s", rec.Body)
	}
	_ = push
}

func TestRemoteAdminActions(t *testing.T) {
	h, f, db, _ := remoteServer(t)
	admin := &principal.Principal{UserID: "a", IsAdmin: true}
	user := &principal.Principal{UserID: "u"}
	for _, rt := range [][2]string{{"POST", "/api/tv/remote/signing-key/rotate"}, {"POST", "/api/tv/remote/webhook-secret"},
		{"POST", "/api/tv/remote/pushes/x/stop"}} {
		if rec := as(h, rt[0], rt[1], ``, user); rec.Code != http.StatusForbidden {
			t.Errorf("%s as a viewer: %d", rt[1], rec.Code)
		}
	}
	rec := as(h, "POST", "/api/tv/remote/signing-key/rotate", ``, admin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"old_deleted":true`) {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
	if _, ok := f.Keys["k1"]; ok {
		t.Error("old key kept at Cloudflare")
	}
	// webhook: refused without the secret, accepted with it; an errored event is recorded
	if rec := as(h, "POST", "/api/tv/remote/webhook", `{}`, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("webhook without a secret configured: %d", rec.Code)
	}
	rec = as(h, "POST", "/api/tv/remote/webhook-secret", ``, admin)
	var sec map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &sec)
	if rec.Code != 200 || len(sec["secret"]) < 32 {
		t.Fatalf("secret: %d %s", rec.Code, rec.Body)
	}
	_, _ = db.Exec(`INSERT INTO tv_remote_inputs (id, cf_uid, name) VALUES ('i1', 'cfin1', 'maktaba-x-1')`)
	ev := `{"name":"Live","data":{"notification_name":"Stream Live Input","input_id":"cfin1","event_type":"live_input.errored","error_code":"ERR_GOP_OUT_OF_RANGE"}}`
	if rec := as(h, "POST", "/api/tv/remote/webhook", ev, nil, WebhookHeader, "wrong"); rec.Code != http.StatusForbidden {
		t.Fatalf("bad secret: %d", rec.Code)
	}
	if rec := as(h, "POST", "/api/tv/remote/webhook", ev, nil, WebhookHeader, sec["secret"]); rec.Code != http.StatusNoContent {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body)
	}
	var code string
	_ = db.QueryRow(`SELECT error FROM tv_remote_inputs WHERE cf_uid = 'cfin1'`).Scan(&code)
	if code != "ERR_GOP_OUT_OF_RANGE" {
		t.Errorf("error code %q", code)
	}
}
