package plugins

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

const machinePlugin = "dev.example.anywhere"

// machineRig is a home (its Cluster routes behind a stand-in for delegation.Accept that
// marks every request as machine node-1's) and a joined machine's tokens and plugin API
// over the same scratch database, reaching the home only through the Remote.
type machineRig struct {
	db          *sql.DB
	exec        func(q string, args ...any)
	home        *httptest.Server
	node        *pl.Server
	nodeTokens  *pl.Tokens
	homeTokens  *pl.Tokens
	video, lib  string
	jobID       int64
	pkg         string
	unsignedURL string
}

func newMachineRig(t *testing.T, placement string) *machineRig {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	rig := &machineRig{}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.QueryRow(`INSERT INTO libraries (name, roots) VALUES ('Films', '{}') RETURNING id::text`).Scan(&rig.lib); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, title, duration_sec)
		VALUES ($1, $2, '/m/a.mp4', 'a.mp4', 10, now(), 'Title', 60) RETURNING id::text`, rig.lib, strings.Repeat("a", 64)).Scan(&rig.video); err != nil {
		t.Fatal(err)
	}
	stage := pl.QueueName(machinePlugin, "tag")
	exec(`INSERT INTO worker_pools (pool, label, scope, size, default_size) VALUES ('plugins','Plugins','global',1,1) ON CONFLICT DO NOTHING`)
	exec(`INSERT INTO job_stages (stage, label, subject_kinds, pool, lane, extension) VALUES ($1, $1, '{video}', 'plugins', 'media', $2)`,
		stage, "plugin:"+machinePlugin)
	if err := db.QueryRow(`INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, video_id)
		VALUES ($1, 'running', 'video', $2, $2) RETURNING id`, stage, rig.video).Scan(&rig.jobID); err != nil {
		t.Fatal(err)
	}
	rig.pkg = filepath.Join(t.TempDir(), machinePlugin, "1.0.0_aaaaaaaaaaaa")
	_ = os.MkdirAll(filepath.Join(rig.pkg, "i18n"), 0o755)
	_ = os.WriteFile(filepath.Join(rig.pkg, "files.json"), []byte(`[{"path":"plugin.wasm","sha256":"x","size":1}]`), 0o644)
	_ = os.WriteFile(filepath.Join(rig.pkg, "plugin.wasm"), []byte("wasm"), 0o644)
	_ = os.WriteFile(filepath.Join(rig.pkg, "secret.txt"), []byte("not listed"), 0o644)
	manifest := `{"id":"` + machinePlugin + `","version":"1.0.0","resources":{"timeout_s":60,"data_mb":1},"placement":"` + placement + `","stage":[{"name":"tag","may_request":[]}]}`
	exec(`INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest) VALUES ($1, '1.0.0', 'd', $2, $3)`,
		machinePlugin, rig.pkg, manifest)
	exec(`INSERT INTO plugin_grants (plugin_id, capabilities, libraries) VALUES ($1, $2, CAST($3 AS uuid[]))`,
		machinePlugin, pq.Array([]string{"library.read", "tags.write"}), pq.Array([]string{rig.lib}))
	exec(`INSERT INTO components (id, want, updated_by) VALUES ($1, 'enabled', 'test')`, "plugin."+machinePlugin)

	rig.homeTokens = pl.NewTokens(db)
	s := svc.NewService(db)
	lc := &pl.Lifecycle{DB: db, Components: s}
	s.Plugins = lc
	t.Cleanup(func() { components.SetLive(nil) })
	if err := pl.ReloadCatalog(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	server := &pl.Server{DB: db, Tokens: rig.homeTokens}
	r := chi.NewRouter()
	(&Cluster{L: lc, Tokens: rig.homeTokens, Server: server}).Mount(r)
	signed := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.ServeHTTP(w, req.WithContext(delegation.WithNode(req.Context(), "node-1")))
	})
	mux := http.NewServeMux()
	mux.Handle("/unsigned/", http.StripPrefix("/unsigned", r))
	mux.Handle("/", signed)
	rig.home = httptest.NewServer(mux)
	t.Cleanup(rig.home.Close)
	rig.unsignedURL = rig.home.URL + "/unsigned"
	rig.nodeTokens = pl.NewTokens(db)
	rig.nodeTokens.Remote = &pl.HTTPRemote{Home: rig.home.URL}
	rig.node = &pl.Server{DB: db, Tokens: rig.nodeTokens, ToLocal: func(p string) string { return "/node" + p }}
	rig.db, rig.exec = db, exec
	return rig
}

func (rig *machineRig) call(t *testing.T, s *pl.Server, token, route string, body any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/plugin-api/v1/"+route, bytes.NewReader(b))
	req.Header.Set("Authorization", "Plugin "+token)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.Code
}

// 54.42 §3 T03: a machine mints its job's token through the home (the home writes the
// invocation row the machine may not), reads and opens media on its own, and every write
// it makes goes to the home, which applies it with its own checks; the home ends the token
// and counts the refusals; an unsigned request gets nothing.
func TestAMachineRunsAPluginThroughTheHome(t *testing.T) {
	rig := newMachineRig(t, "any")
	ctx := context.Background()
	m, err := rig.nodeTokens.Mint(ctx, rig.jobID)
	if err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := rig.db.QueryRow(`SELECT count(*) FROM plugin_invocations WHERE id = $1`, m.InvocationID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("the home's invocation row: %d %v", rows, err)
	}
	vid := map[string]string{"kind": "video", "id": rig.video}
	if code, _ := rig.call(t, rig.node, m.Token, "library/video", map[string]string{"id": rig.video}); code != 200 {
		t.Fatalf("a read on the machine: %d", code)
	}
	if code, c := rig.call(t, rig.node, m.Token, "write/add-tags", map[string]any{"video": rig.video, "tags": []string{"Night"}}); code != 200 {
		t.Fatalf("a forwarded write: %d %s", code, c)
	}
	var tagged, stamped int
	_ = rig.db.QueryRow(`SELECT count(*) FROM plugin_writes WHERE plugin_id = $1`, machinePlugin).Scan(&tagged)
	_ = rig.db.QueryRow(`SELECT count(*) FROM plugin_write_lineage WHERE subject_id = $1`, rig.video).Scan(&stamped)
	if tagged == 0 || stamped != 1 {
		t.Fatalf("the home applied it: writes %d lineage %d", tagged, stamped)
	}
	// the home checks a forwarded write as its own: no grant, no write
	if code, c := rig.call(t, rig.node, m.Token, "write/set-field", map[string]any{"video": rig.video, "field": "description", "value": "x"}); code != 403 || c != pl.CodeCapability {
		t.Fatalf("an ungranted write: %d %s", code, c)
	}
	if code, c := rig.call(t, rig.node, m.Token, "data/put", map[string]any{"subject": vid, "key": "k", "value": "1"}); code != 200 {
		t.Fatalf("data on the home: %d %s", code, c)
	}
	// the home's models and their daily share are the home's (story 54.21)
	for _, route := range []string{"compute/embed", "compute/generate"} {
		if !pl.IsForwarded(route) {
			t.Fatalf("%s is answered on the machine", route)
		}
	}
	ended, refused := rig.nodeTokens.EndCounting(ctx, m.InvocationID)
	if !ended || refused < 1 {
		t.Fatalf("end: %v %d", ended, refused)
	}
	var endedAt sql.NullTime
	_ = rig.db.QueryRow(`SELECT ended_at FROM plugin_invocations WHERE id = $1`, m.InvocationID).Scan(&endedAt)
	if !endedAt.Valid {
		t.Fatal("the home did not end the invocation")
	}
	resp, err := http.Post(rig.unsignedURL+pl.ClusterMintPath, "application/json", strings.NewReader(`{"job_id":1}`))
	if err != nil || resp.StatusCode != 401 {
		t.Fatalf("unsigned: %v %v", resp, err)
	}
	_ = resp.Body.Close()
}

// 54.42 T05: turning the plugin off on the home reaches the machine at its next call,
// and a plugin placed on the home is never minted for a machine.
func TestOffAndHomeOnlyReachTheMachine(t *testing.T) {
	rig := newMachineRig(t, "any")
	ctx := context.Background()
	m, err := rig.nodeTokens.Mint(ctx, rig.jobID)
	if err != nil {
		t.Fatal(err)
	}
	rig.exec(`UPDATE components SET want = 'installed' WHERE id = $1`, "plugin."+machinePlugin)
	if code, c := rig.call(t, rig.node, m.Token, "library/video", map[string]string{"id": rig.video}); code != 401 || c != pl.CodeToken {
		t.Fatalf("after off: %d %s", code, c)
	}
	if _, err := rig.nodeTokens.Mint(ctx, rig.jobID); err == nil || !strings.Contains(err.Error(), pl.CodePluginOff) {
		t.Fatalf("a mint for an off plugin: %v", err)
	}
	home := newMachineRig(t, "home")
	if _, err := home.nodeTokens.Mint(ctx, home.jobID); err == nil || !strings.Contains(err.Error(), pl.CodeScope) {
		t.Fatalf("a home plugin on a machine: %v", err)
	}
}

// 54.42 §4: a machine's job outcomes trip the breaker on the home, with the dispatcher's
// own statements (held equal to breaker.py's).
func TestMachineOutcomesReachTheBreaker(t *testing.T) {
	rig := newMachineRig(t, "any")
	r := chi.NewRouter()
	r.Post("/internal/plugins/outcome", Outcome(rig.nodeTokens.Remote))
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("local"), 0o600)
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", tok)
	var last string
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/internal/plugins/outcome",
			strings.NewReader(`{"plugin_id":"`+machinePlugin+`","cause":"plugin_trap","ms":5}`))
		req.Header.Set(middleware.LocalTokenHeader, "local")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("outcome %d: %d %s", i, w.Code, w.Body)
		}
		var out struct {
			Tripped string `json:"tripped"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		last = out.Tripped
	}
	var tripped bool
	var crashes int
	_ = rig.db.QueryRow(`SELECT tripped, jsonb_array_length(crashes) FROM plugin_health WHERE plugin_id = $1`, machinePlugin).Scan(&tripped, &crashes)
	if last != "crashes" || !tripped || crashes != 5 {
		t.Fatalf("breaker: %q tripped=%v crashes=%d", last, tripped, crashes)
	}
	var hold sql.NullString
	_ = rig.db.QueryRow(`SELECT hold_by FROM components WHERE id = $1`, "plugin."+machinePlugin).Scan(&hold)
	if hold.String != "plugin-breaker" {
		t.Fatalf("hold %v", hold)
	}
}

// 54.42 §2: the home serves a package's listed files to a signed machine, nothing else.
func TestPackageFilesForMachines(t *testing.T) {
	rig := newMachineRig(t, "any")
	get := func(base, p string) int {
		resp, err := http.Get(base + pl.ClusterFilesPrefix + machinePlugin + "/" + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	folder := filepath.Base(rig.pkg)
	for p, want := range map[string]int{
		folder + "/files.json":   200,
		folder + "/plugin.wasm":  200,
		folder + "/secret.txt":   404,
		folder + "/../x":         400,
		"0.9.0_bbbb/plugin.wasm": 404,
	} {
		if got := get(rig.home.URL, p); got != want {
			t.Errorf("%s: %d, want %d", p, got, want)
		}
	}
	if got := get(rig.unsignedURL, folder+"/plugin.wasm"); got != 401 {
		t.Errorf("unsigned: %d", got)
	}
}

// The home's breaker statements are the dispatcher's, character for character.
func TestBreakerSQLIsTheDispatchers(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "pipeline", "src", "maktaba_pipeline", "plugins", "breaker.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"_RECORD_PG", "_TRIP_PG"} {
		m := regexp.MustCompile(`(?s)` + name + ` = """\n(.*?)"""`).FindSubmatch(src)
		if m == nil || !strings.Contains(pl.BreakerSQL(), string(m[1])) {
			t.Errorf("%s differs from breaker.py's", name)
		}
	}
	_ = time.Now
}
