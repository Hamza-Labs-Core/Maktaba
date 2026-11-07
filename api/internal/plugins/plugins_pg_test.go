package plugins

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

const testPlugin = "dev.example.tagger"

type fixture struct {
	db     *sql.DB
	s      *Server
	libA   string
	libB   string
	videoA string // the job's subject, in library A
	videoB string // another video in library A
	videoC string // a video in library B
	jobID  int64
	file   string
}

func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	f := &fixture{db: db}
	dir := t.TempDir()
	f.file = filepath.Join(dir, "a.mp4")
	if err := os.WriteFile(f.file, bytes.Repeat([]byte("0123456789"), 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(filepath.Join(pkg, "i18n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(pkg, "i18n", "en.json"), []byte(`{"notice.done":"Done"}`), 0o644)
	for _, l := range []*string{&f.libA, &f.libB} {
		if err := db.QueryRow(`INSERT INTO libraries (name, roots) VALUES ($1, '{}') RETURNING id::text`, fmt.Sprint("L", len(*l), time.Now().UnixNano())).Scan(l); err != nil {
			t.Fatal(err)
		}
	}
	video := func(lib, hash string) string {
		var id string
		if err := db.QueryRow(`INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, title, duration_sec)
			VALUES ($1,$2,$3,'a.mp4',10000,now(),'Title',60) RETURNING id::text`, lib, hash, f.file).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.videoA, f.videoB, f.videoC = video(f.libA, strings.Repeat("a", 64)), video(f.libA, strings.Repeat("b", 64)),
		video(f.libB, strings.Repeat("c", 64))
	stage := QueueName(testPlugin, "tag")
	other := QueueName(testPlugin, "more")
	f.exec(t, `INSERT INTO worker_pools (pool, label, scope, size, default_size) VALUES ('plugins','Plugins','global',1,1) ON CONFLICT DO NOTHING`)
	for _, s := range []string{stage, other} {
		f.exec(t, `INSERT INTO job_stages (stage, label, subject_kinds, pool, lane, extension, may_request)
			VALUES ($1, $1, '{video}', 'plugins', 'media', $2, $3)`, s, "plugin:"+testPlugin, pq.Array([]string{other}))
	}
	if err := db.QueryRow(`INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, video_id) VALUES ($1,'running','video',$2,$2) RETURNING id`,
		stage, f.videoA).Scan(&f.jobID); err != nil {
		t.Fatal(err)
	}
	manifest := `{"resources":{"timeout_s":60,"data_mb":1},"stage":[{"name":"tag","may_request":["more"]},{"name":"more","may_request":[]}]}`
	f.exec(t, `INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest) VALUES ($1,'1.0.0','d',$2,$3)`, testPlugin, pkg, manifest)
	f.grant(t, []string{"library.read", "tags.write", "metadata.write", "media.read", "jobs.enqueue", "notify.admin", "suggest"}, []string{f.libA})
	f.s = &Server{DB: db, Tokens: NewTokens(db), ArtifactsDir: filepath.Join(dir, "artifacts")}
	return f
}

func (f *fixture) grant(t *testing.T, caps, libs []string) {
	t.Helper()
	f.exec(t, `INSERT INTO plugin_grants (plugin_id, capabilities, libraries) VALUES ($1,$2,CAST($3 AS uuid[]))
		ON CONFLICT (plugin_id) DO UPDATE SET capabilities = EXCLUDED.capabilities, libraries = EXCLUDED.libraries,
		generation = plugin_grants.generation + 1`, testPlugin, pq.Array(caps), pq.Array(libs))
}

func (f *fixture) mint(t *testing.T) *Minted {
	t.Helper()
	m, err := f.s.Tokens.Mint(context.Background(), f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fixture) call(t *testing.T, token, route string, body any) (int, string, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/plugin-api/v1/"+route, bytes.NewReader(b))
	req.Header.Set("Authorization", "Plugin "+token)
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, req)
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.Code, w.Body.Bytes()
}

// T03: grants come from plugin_grants only; T05: ended and unknown tokens are 401.
func TestMintAndTokens(t *testing.T) {
	f := newFixture(t)
	m := f.mint(t)
	if m.PluginID != testPlugin || len(m.Grants) != 7 || len(m.Libraries) != 1 || m.Libraries[0] != f.libA {
		t.Fatalf("minted %+v", m)
	}
	var stored string
	if err := f.db.QueryRow(`SELECT token_hash FROM plugin_invocations WHERE id = $1`, m.InvocationID).Scan(&stored); err != nil || stored == m.Token {
		t.Fatalf("the token itself was stored (%v)", err)
	}
	if code, _, _ := f.call(t, m.Token, "job/grants", map[string]any{}); code != 200 {
		t.Fatalf("job/grants %d", code)
	}
	if code, c, _ := f.call(t, "nope", "job/grants", map[string]any{}); code != 401 || c != CodeToken {
		t.Fatalf("unknown token %d %s", code, c)
	}
	f.s.Tokens.End(context.Background(), m.InvocationID)
	if code, c, _ := f.call(t, m.Token, "job/grants", map[string]any{}); code != 401 || c != CodeToken {
		t.Fatalf("ended token %d %s", code, c)
	}
	m2 := f.mint(t)
	f.s.Tokens.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if code, c, _ := f.call(t, m2.Token, "job/grants", map[string]any{}); code != 401 || c != CodeToken {
		t.Fatalf("expired token %d %s", code, c)
	}
}

// T01, T02: capability and scope, refused and counted.
func TestCapabilityAndScope(t *testing.T) {
	f := newFixture(t)
	tok := f.mint(t).Token
	if code, c, _ := f.call(t, tok, "transcript/segments", map[string]any{"video": f.videoA}); code != 403 || c != CodeCapability {
		t.Fatalf("no transcript.read: %d %s", code, c)
	}
	if code, _, _ := f.call(t, tok, "library/video", map[string]any{"id": f.videoB}); code != 200 {
		t.Fatalf("a video in a granted library: %d", code)
	}
	if code, c, _ := f.call(t, tok, "library/video", map[string]any{"id": f.videoC}); code != 403 || c != CodeScope {
		t.Fatalf("library B: %d %s", code, c)
	}
	if code, c, _ := f.call(t, tok, "write/add-tags", map[string]any{"video": f.videoB, "tags": []string{"x"}}); code != 403 || c != CodeScope {
		t.Fatalf("a write outside the job's subject: %d %s", code, c)
	}
	if code, _, _ := f.call(t, tok, "write/add-tags", map[string]any{"video": f.videoA, "tags": []string{"Plugin Tag"}}); code != 200 {
		t.Fatalf("add-tags on the subject: %d", code)
	}
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM plugin_writes WHERE plugin_id = $1 AND target = 'video_tags'`, testPlugin).Scan(&n)
	if n != 1 {
		t.Fatalf("plugin_writes rows %d", n)
	}
}

// T04: a grant revoked mid-job answers plugin_grants_changed on the next call.
func TestRevokeMidJob(t *testing.T) {
	f := newFixture(t)
	tok := f.mint(t).Token
	if code, _, _ := f.call(t, tok, "library/video", map[string]any{"id": f.videoA}); code != 200 {
		t.Fatal(code)
	}
	f.grant(t, []string{"tags.write"}, []string{f.libA})
	if code, c, _ := f.call(t, tok, "library/video", map[string]any{"id": f.videoA}); code != 401 || c != CodeGrantsChanged {
		t.Fatalf("after revoke: %d %s", code, c)
	}
}

// T06: set-field writes allow-listed fields with a plugin_writes row; others are refused.
func TestSetField(t *testing.T) {
	f := newFixture(t)
	tok := f.mint(t).Token
	if code, _, _ := f.call(t, tok, "write/set-field", map[string]any{"video": f.videoA, "field": "description", "value": "new"}); code != 200 {
		t.Fatal(code)
	}
	if code, _, _ := f.call(t, tok, "write/set-field", map[string]any{"video": f.videoA, "field": "year", "value": "1999"}); code != 200 {
		t.Fatal(code)
	}
	var desc, year string
	_ = f.db.QueryRow(`SELECT description, metadata->>'year' FROM videos WHERE id = $1`, f.videoA).Scan(&desc, &year)
	if desc != "new" || year != "1999" {
		t.Fatalf("description %q year %q", desc, year)
	}
	for _, field := range []string{"title", "state", "path"} {
		if code, c, _ := f.call(t, tok, "write/set-field", map[string]any{"video": f.videoA, "field": field, "value": "x"}); code != 403 || c != CodeField {
			t.Fatalf("%s: %d %s", field, code, c)
		}
	}
	var before sql.NullString
	_ = f.db.QueryRow(`SELECT before::text FROM plugin_writes WHERE field = 'description'`).Scan(&before)
	if before.String != "null" {
		t.Fatalf("before %q", before.String)
	}
}

// T12: queue/request only to the calling stage's may_request.
func TestQueueRequest(t *testing.T) {
	f := newFixture(t)
	tok := f.mint(t).Token
	if code, _, _ := f.call(t, tok, "queue/request", map[string]any{"stage": "more", "subject": map[string]string{"kind": "video", "id": f.videoA}}); code != 200 {
		t.Fatalf("own allowed stage %d", code)
	}
	for _, stage := range []string{"tag", "transcribe", "../more"} {
		if code, c, _ := f.call(t, tok, "queue/request", map[string]any{"stage": stage, "subject": map[string]string{"kind": "video", "id": f.videoA}}); code != 403 || c != CodeScope {
			t.Fatalf("%s: %d %s", stage, code, c)
		}
	}
}

// data: the plugin's namespace and its quota.
func TestDataQuota(t *testing.T) {
	f := newFixture(t)
	tok := f.mint(t).Token
	sub := map[string]string{"kind": "video", "id": f.videoA}
	if code, _, _ := f.call(t, tok, "data/put", map[string]any{"subject": sub, "key": "k", "value": `{"a":1}`}); code != 200 {
		t.Fatal(code)
	}
	_, _, body := f.call(t, tok, "data/get", map[string]any{"subject": sub, "key": "k"})
	if !strings.Contains(string(body), `\"a\": 1`) && !strings.Contains(string(body), `{\"a\":1}`) {
		t.Fatalf("get %s", body)
	}
	big := `"` + strings.Repeat("x", 60<<10) + `"`
	// 17 values of 60 KB fill all but the last 60 KB of the 1 MB quota
	for i := 0; i < 17; i++ {
		f.call(t, tok, "data/put", map[string]any{"subject": sub, "key": fmt.Sprint("big", i), "value": big})
	}
	if code, c, _ := f.call(t, tok, "data/put", map[string]any{"subject": sub, "key": "last", "value": big}); code != 403 || c != CodeQuota {
		t.Fatalf("over 1 MB: %d %s", code, c)
	}
	if code, c, _ := f.call(t, tok, "data/put", map[string]any{"subject": sub, "key": "huge", "value": `"` + strings.Repeat("x", 70<<10) + `"`}); code != 413 || c != CodeTooLarge {
		t.Fatalf("over 64 KB: %d %s", code, c)
	}
}

// 54.05 T01-T04, T08: media handles: bytes equal, scope, budget, a changed file.
func TestMediaHandles(t *testing.T) {
	f := newFixture(t)
	m := f.mint(t)
	tok := m.Token
	if code, c, _ := f.call(t, tok, "media/open", map[string]any{"video": f.videoB}); code != 403 || c != CodeScope {
		t.Fatalf("not the subject: %d %s", code, c)
	}
	_, _, body := f.call(t, tok, "media/open", map[string]any{"video": f.videoA})
	var h struct {
		Handle uint64 `json:"handle"`
	}
	_ = json.Unmarshal(body, &h)
	code, _, data := f.call(t, tok, "media/read", map[string]any{"handle": h.Handle, "offset": 10, "len": 20})
	if code != 200 || string(data) != "01234567890123456789" {
		t.Fatalf("read %d %q", code, data)
	}
	for i := 0; i < 2; i++ {
		f.call(t, tok, "media/read", map[string]any{"handle": h.Handle, "offset": 0, "len": 9000})
	}
	if code, c, _ := f.call(t, tok, "media/read", map[string]any{"handle": h.Handle, "offset": 0, "len": 9000}); code != 403 || c != CodeQuota {
		t.Fatalf("over twice the file: %d %s", code, c)
	}
	_, _, body = f.call(t, tok, "media/open", map[string]any{"video": f.videoA})
	_ = json.Unmarshal(body, &h)
	if err := os.WriteFile(f.file, []byte("replaced"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, c, _ := f.call(t, tok, "media/read", map[string]any{"handle": h.Handle, "offset": 0, "len": 4}); code != 409 || c != CodeMediaChanged {
		t.Fatalf("changed file: %d %s", code, c)
	}
	f.s.Tokens.End(context.Background(), m.InvocationID)
	if code, _, _ := f.call(t, tok, "media/size", map[string]any{"handle": h.Handle}); code != 401 {
		t.Fatalf("after the job: %d", code)
	}
	if code, c, _ := f.call(t, f.mint(t).Token, "media/frame", map[string]any{"video": f.videoA, "t_ms": 0, "max_edge": 100}); code != 403 || c != CodeCapability {
		t.Fatalf("frames without media.frames: %d %s", code, c)
	}
}

func TestNotifyAndSuggest(t *testing.T) {
	f := newFixture(t)
	tok := f.mint(t).Token
	if code, c, _ := f.call(t, tok, "notify/admin", map[string]any{"code": "unknown"}); code != 400 {
		t.Fatalf("a code not in the strings: %d %s", code, c)
	}
	for i := 0; i < MaxNotices; i++ {
		if code, _, _ := f.call(t, tok, "notify/admin", map[string]any{"code": "done"}); code != 200 {
			t.Fatalf("notice %d: %d", i, code)
		}
	}
	if code, c, _ := f.call(t, tok, "notify/admin", map[string]any{"code": "done"}); code != 403 || c != CodeQuota {
		t.Fatalf("11th notice: %d %s", code, c)
	}
	if code, _, _ := f.call(t, tok, "suggest/add", map[string]any{"subject": map[string]string{"kind": "video", "id": f.videoA},
		"field": "title", "value": "Better", "reason": "x"}); code != 200 {
		t.Fatal(code)
	}
}

// The plugin API on a real unix socket.
func TestListenUnix(t *testing.T) {
	f := newFixture(t)
	sock := filepath.Join(t.TempDir(), "plugin-api.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ListenUnix(ctx, sock, f.s) }()
	var c *http.Client
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (netConn, error) {
		return dialer.DialContext(ctx, "unix", sock)
	}}}
	req, _ := http.NewRequest(http.MethodPost, "http://plugin-api/plugin-api/v1/job/grants", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Plugin "+f.mint(t).Token)
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

type netConn = net.Conn

var dialer net.Dialer

// 54.09 §4 (#711): the plugins' job numbers on /metrics, read from plugin_health at the
// scrape: jobs by outcome, p50 and p95 of the last jobs' time, their memory peak.
func TestHealthMetrics(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	recent := `[{"at":1,"cause":"","ms":1000,"memory":1048576},{"at":2,"cause":"","ms":3000,"memory":4194304},
		{"at":3,"cause":"plugin_trap","ms":2000,"memory":2097152},{"at":4,"cause":"","ms":10000,"memory":3145728}]`
	if _, err := db.Exec(`INSERT INTO plugin_health (plugin_id, recent, jobs_done, jobs_failed) VALUES ($1, $2, 3, 1),
		('dev.example.idle', '[]', 0, 0)`, testPlugin, recent); err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	healthDB.Store(db)
	t.Cleanup(func() { healthDB.Store(nil) })
	reg.MustRegister(healthCollector{})
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, f := range fams {
		for _, m := range f.GetMetric() {
			key := f.GetName()
			for _, l := range m.GetLabel() {
				key += "," + l.GetName() + "=" + l.GetValue()
			}
			if m.GetCounter() != nil {
				got[key] = m.GetCounter().GetValue()
			} else {
				got[key] = m.GetGauge().GetValue()
			}
		}
	}
	for key, want := range map[string]float64{
		"maktaba_plugin_jobs_total,outcome=ok,plugin=" + testPlugin:          3,
		"maktaba_plugin_jobs_total,outcome=failed,plugin=" + testPlugin:      1,
		"maktaba_plugin_job_seconds,plugin=" + testPlugin + ",quantile=0.5":  2,
		"maktaba_plugin_job_seconds,plugin=" + testPlugin + ",quantile=0.95": 10,
		"maktaba_plugin_memory_peak_bytes,plugin=" + testPlugin:              4194304,
		"maktaba_plugin_jobs_total,outcome=ok,plugin=dev.example.idle":       0,
	} {
		if v, ok := got[key]; !ok || v != want {
			t.Errorf("%s = %v (present %v), want %v", key, v, ok, want)
		}
	}
	if _, ok := got["maktaba_plugin_job_seconds,plugin=dev.example.idle,quantile=0.5"]; ok {
		t.Error("a plugin with no recent job has no job time")
	}
}
