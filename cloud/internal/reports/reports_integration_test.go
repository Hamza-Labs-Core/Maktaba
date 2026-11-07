//go:build integration

package reports

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// Against Postgres (MAKTABA_CLOUD_DB_URL), in a scratch database of its own.
func testDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	admin, err := db.Open(url, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "cloud_rep_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := admin.DB.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := db.Open(u.String(), 4, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_, _ = admin.DB.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})
	if err := db.NewMigrator(pool, migrations.FS, ".").Up(ctx); err != nil {
		t.Fatal(err)
	}
	return pool.DB, ctx
}

// home makes an account and its linked home: the server's id.
func home(t *testing.T, ctx context.Context, d *sql.DB, slug string) string {
	t.Helper()
	var uid, sid string
	if err := d.QueryRowContext(ctx, `INSERT INTO users (email, password_hash, display_name) VALUES ($1, 'x', 'Owner') RETURNING id`,
		slug+"@example.org").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, $2, $2, 'x') RETURNING id`,
		uid, slug).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	return sid
}

// secrets is the server secret check: "good" is every home's secret.
type secrets struct{}

func (secrets) VerifySecret(_ context.Context, _, secret string) error {
	if secret != "good" {
		return errors.New("bad secret")
	}
	return nil
}

type rig struct {
	t     *testing.T
	ctx   context.Context
	db    *sql.DB
	store *Store
	agent *AgentDeps
	h     http.Handler
	now   time.Time
}

func newRig(t *testing.T, admitted bool) *rig {
	d, ctx := testDB(t)
	rg := &rig{t: t, ctx: ctx, db: d, now: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
	rg.store = &Store{DB: d, Now: func() time.Time { return rg.now }}
	rg.agent = &AgentDeps{Store: rg.store, Servers: secrets{}}
	r := chi.NewRouter()
	MountAgent(r, rg.agent)
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !admitted {
				writeErr(w, http.StatusForbidden, "admin_access_denied", "")
				return
			}
			next.ServeHTTP(w, req)
		})
	}
	MountAdmin(r, &AdminDeps{Store: rg.store, DB: d, Gate: gate})
	rg.h = r
	return rg
}

func (rg *rig) do(method, path, secret string, body []byte, ctype string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if secret != "" {
		id := strings.Split(strings.TrimPrefix(path, "/v1/homes/"), "/")[0]
		req.SetBasicAuth(id, secret)
	}
	rec := httptest.NewRecorder()
	rg.h.ServeHTTP(rec, req)
	return rec
}

func form(t *testing.T, report map[string]any, shot []byte) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	raw, _ := json.Marshal(report)
	_ = mw.WriteField("report", string(raw))
	if shot != nil {
		fw, _ := mw.CreateFormFile("screenshot", "screenshot.png")
		_, _ = fw.Write(shot)
	}
	_ = mw.Close()
	return b.Bytes(), mw.FormDataContentType()
}

func sample(id int) map[string]any {
	return map[string]any{"id": id, "when": "2026-11-07T11:50:00Z", "app": "apple.player", "version": "0.3.95",
		"platform": "ios", "category": "playback", "text": "the video stopped",
		"lines":  []any{map[string]any{"msg": "segment_missing", "fp": "fp-1"}},
		"server": []any{map[string]any{"msg": "stream_failed", "fp": "fp-2"}, map[string]any{"msg": "x", "fp": "fp-1"}}}
}

func TestAHomeSendsAReportOnceAndOnlyWithItsSecret(t *testing.T) {
	rg := newRig(t, true)
	sid := home(t, rg.ctx, rg.db, "homea")
	body, ct := form(t, sample(7), []byte("\x89PNG\r\n\x1a\nshot"))
	if rec := rg.do("POST", "/v1/homes/"+sid+"/reports", "wrong", body, ct); rec.Code != 401 {
		t.Fatalf("a wrong secret: %d", rec.Code)
	}
	rec := rg.do("POST", "/v1/homes/"+sid+"/reports", "good", body, ct)
	if rec.Code != 201 {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	var got struct{ ID int64 }
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	// the same report again (an answer the home never got): the same row
	again := rg.do("POST", "/v1/homes/"+sid+"/reports", "good", body, ct)
	var second struct{ ID int64 }
	_ = json.Unmarshal(again.Body.Bytes(), &second)
	if again.Code != 200 || second.ID != got.ID {
		t.Fatalf("sent again: %d, id %d then %d", again.Code, got.ID, second.ID)
	}
	rep, err := rg.store.Get(rg.ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.HomeReportID != 7 || rep.App != "apple.player" || rep.HomeName != "homea" || rep.OwnerEmail != "homea@example.org" ||
		!rep.Screenshot || strings.Join(rep.Fingerprints, ",") != "fp-1,fp-2" {
		t.Fatalf("kept: %+v", rep.Summary)
	}
	// a screenshot over 1 MB is refused
	big, ct2 := form(t, sample(8), bytes.Repeat([]byte("x"), MaxScreenshot+1))
	if rec := rg.do("POST", "/v1/homes/"+sid+"/reports", "good", big, ct2); rec.Code != 413 {
		t.Fatalf("a 1 MB+ screenshot: %d", rec.Code)
	}
	// the rollback: 503 and the home keeps it
	rg.agent.Refuse = true
	b9, ct9 := form(t, sample(9), nil)
	if rec := rg.do("POST", "/v1/homes/"+sid+"/reports", "good", b9, ct9); rec.Code != 503 {
		t.Fatalf("refused: %d", rec.Code)
	}
}

func TestTheServiceAdminTriagesAndRepliesAndTheHomeFetchesIt(t *testing.T) {
	rg := newRig(t, true)
	a, b := home(t, rg.ctx, rg.db, "homea"), home(t, rg.ctx, rg.db, "homeb")
	for _, s := range []struct {
		sid string
		n   int
	}{{a, 1}, {a, 2}, {b, 1}} {
		body, ct := form(t, sample(s.n), nil)
		if rec := rg.do("POST", "/v1/homes/"+s.sid+"/reports", "good", body, ct); rec.Code != 201 {
			t.Fatalf("send: %d", rec.Code)
		}
	}
	list := rg.do("GET", "/v1/admin/reports?app=apple.player&fp=fp-2", "", nil, "")
	var l struct {
		Reports []Summary
		Total   int
	}
	_ = json.Unmarshal(list.Body.Bytes(), &l)
	if list.Code != 200 || l.Total != 3 || len(l.Reports) != 3 {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	first := l.Reports[2].ID // oldest: home a's first
	post := func(path, body string) int {
		return rg.do("POST", "/v1/admin/reports/"+strconv.FormatInt(first, 10)+path, "", []byte(body), "application/json").Code
	}
	if c := post("/status", `{"status":"fixed","fixed_in":"0.3.96"}`); c != 204 {
		t.Fatalf("status: %d", c)
	}
	if c := post("/status", `{"status":"gone"}`); c != 400 {
		t.Fatalf("a bad status: %d", c)
	}
	if c := post("/issue", `{"url":"https://github.com/Hamza-Labs-Core/Maktaba/issues/1"}`); c != 204 {
		t.Fatalf("issue: %d", c)
	}
	if c := post("/issue", `{"url":"javascript:alert(1)"}`); c != 400 {
		t.Fatalf("a bad link: %d", c)
	}
	rg.now = rg.now.Add(time.Minute)
	if c := post("/reply", `{"reply":"Fixed in 0.3.96, thank you."}`); c != 204 {
		t.Fatalf("reply: %d", c)
	}
	var audits int
	_ = rg.db.QueryRow(`SELECT count(*) FROM audit_log WHERE target_kind = 'problem_report'`).Scan(&audits)
	if audits != 3 {
		t.Fatalf("audit rows: %d", audits)
	}
	// home a fetches its answer; home b has none
	rec := rg.do("GET", "/v1/homes/"+a+"/reports/replies?since=2026-11-07T00:00:00Z", "good", nil, "")
	var rs struct{ Replies []Reply }
	_ = json.Unmarshal(rec.Body.Bytes(), &rs)
	if rec.Code != 200 || len(rs.Replies) != 1 || rs.Replies[0].HomeReportID != 1 || rs.Replies[0].Status != "fixed" ||
		rs.Replies[0].FixedIn != "0.3.96" {
		t.Fatalf("home a's replies: %d %s", rec.Code, rec.Body)
	}
	after := rs.Replies[0].RepliedAt.Format(time.RFC3339Nano)
	if rec := rg.do("GET", "/v1/homes/"+a+"/reports/replies?since="+neturl.QueryEscape(after), "good", nil, ""); !strings.Contains(rec.Body.String(), `"replies":[]`) {
		t.Fatalf("nothing new after the last: %s", rec.Body)
	}
	if rec := rg.do("GET", "/v1/homes/"+b+"/reports/replies", "good", nil, ""); !strings.Contains(rec.Body.String(), `"replies":[]`) {
		t.Fatalf("home b: %s", rec.Body)
	}
	// home a's admin: "Delete what Maktaba has"
	rec = rg.do("DELETE", "/v1/homes/"+a+"/reports", "good", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deleted":2`) {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, total, _ := rg.store.List(rg.ctx, Filter{}); total != 1 {
		t.Fatalf("left: %d", total)
	}
}

func TestOnlyTheServiceAdminReadsReports(t *testing.T) {
	rg := newRig(t, false)
	for _, p := range []string{"/v1/admin/reports", "/v1/admin/reports/1", "/v1/admin/reports/1/screenshot"} {
		if rec := rg.do("GET", p, "", nil, ""); rec.Code != 403 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
	if rec := rg.do("POST", "/v1/admin/reports/1/reply", "", []byte(`{"reply":"x"}`), "application/json"); rec.Code != 403 {
		t.Fatalf("reply: %d", rec.Code)
	}
}

func TestAReportGoesAfter90Days(t *testing.T) {
	rg := newRig(t, true)
	sid := home(t, rg.ctx, rg.db, "homea")
	body, ct := form(t, sample(1), nil)
	rg.do("POST", "/v1/homes/"+sid+"/reports", "good", body, ct)
	rg.now = rg.now.Add(89 * 24 * time.Hour)
	if n, err := rg.store.Sweep(rg.ctx); err != nil || n != 0 {
		t.Fatalf("at 89 days: %d %v", n, err)
	}
	rg.now = rg.now.Add(2 * 24 * time.Hour)
	if n, err := rg.store.Sweep(rg.ctx); err != nil || n != 1 {
		t.Fatalf("at 91 days: %d %v", n, err)
	}
}
