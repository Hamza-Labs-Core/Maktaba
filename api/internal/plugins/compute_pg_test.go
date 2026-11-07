package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// Story 54.21: compute.embed and compute.llm through the plugin API.

type fakeEmbedder struct {
	calls int
	off   bool
}

func (e *fakeEmbedder) EmbedTexts(_ context.Context, texts []string) (string, [][]float32, error) {
	e.calls++
	if e.off {
		return "", nil, ErrComputeOff
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{float32(i), 0.5, 0.25}
	}
	return "test-e5", out, nil
}

// fakeOllama answers /api/generate with the next answer of answers (the last one again).
type fakeOllama struct {
	mu      sync.Mutex
	answers []string
	asked   []map[string]any
}

func (o *fakeOllama) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	_ = json.NewDecoder(r.Body).Decode(&in)
	o.mu.Lock()
	o.asked = append(o.asked, in)
	a := o.answers[0]
	if len(o.answers) > 1 {
		o.answers = o.answers[1:]
	}
	o.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"model": in["model"], "response": a, "done": true})
}

func (f *fixture) setRules(t *testing.T, section string, v map[string]any) {
	t.Helper()
	b, _ := json.Marshal(v)
	f.exec(t, `INSERT INTO app_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		"rules."+section, string(b))
}

func computeFixture(t *testing.T, caps ...string) (*fixture, *fakeEmbedder, *fakeOllama, *httptest.Server) {
	t.Helper()
	f := newFixture(t)
	f.grant(t, caps, []string{f.libA})
	emb := &fakeEmbedder{}
	ol := &fakeOllama{answers: []string{"a summary"}}
	srv := httptest.NewServer(ol)
	t.Cleanup(srv.Close)
	f.s.Compute = &Compute{Embedder: emb, LeaseWait: 5 * time.Millisecond}
	f.setRules(t, "plugin_compute", map[string]any{"url": srv.URL, "model": "qwen2.5:7b"})
	return f, emb, ol, srv
}

// T01: 10 texts give 10 vectors of the embedder's dimension and its model id; the limits hold.
func TestComputeEmbed(t *testing.T) {
	f, emb, _, _ := computeFixture(t, "compute.embed")
	tok := f.mint(t).Token
	texts := make([]string, 10)
	for i := range texts {
		texts[i] = "نص"
	}
	code, _, body := f.call(t, tok, "compute/embed", map[string]any{"texts": texts})
	var out struct {
		Model   string      `json:"model"`
		Vectors [][]float32 `json:"vectors"`
	}
	_ = json.Unmarshal(body, &out)
	if code != 200 || out.Model != "test-e5" || len(out.Vectors) != 10 || len(out.Vectors[3]) != 3 || emb.calls != 1 {
		t.Fatalf("embed = %d %s (calls %d)", code, body, emb.calls)
	}
	if code, c, _ := f.call(t, tok, "compute/embed", map[string]any{"texts": make([]string, 65)}); code != 413 || c != CodeTooLarge {
		t.Fatalf("65 texts = %d %s", code, c)
	}
	if code, c, _ := f.call(t, tok, "compute/embed", map[string]any{"texts": []string{strings.Repeat("ع", 2001)}}); code != 413 || c != CodeTooLarge {
		t.Fatalf("2,001 characters = %d %s", code, c)
	}
	if code, c, _ := f.call(t, tok, "compute/generate", map[string]any{"prompt": "x", "max_tokens": 10}); code != 403 || c != CodeCapability {
		t.Fatalf("generate without compute.llm = %d %s", code, c)
	}
	emb.off = true
	if code, c, _ := f.call(t, tok, "compute/embed", map[string]any{"texts": []string{"a"}}); code != 503 || c != CodeComputeUnavailable {
		t.Fatalf("embed while search.semantic is off = %d %s", code, c)
	}
}

// T02: llm off answers unavailable (the plugin keeps running); on, the model's text; a
// json-schema answer the model fails twice is compute_bad_output, once is retried.
func TestComputeGenerate(t *testing.T) {
	f, _, ol, _ := computeFixture(t, "compute.llm")
	tok := f.mint(t).Token
	on := false
	f.s.Compute.LLMOn = func(context.Context) bool { return on }
	ask := map[string]any{"prompt": "summarise", "max_tokens": 200, "temperature": 0.2}
	if code, c, _ := f.call(t, tok, "compute/generate", ask); code != 503 || c != CodeComputeUnavailable {
		t.Fatalf("llm off = %d %s", code, c)
	}
	on = true
	code, _, body := f.call(t, tok, "compute/generate", ask)
	if code != 200 || !strings.Contains(string(body), "a summary") {
		t.Fatalf("generate = %d %s", code, body)
	}
	if got := ol.asked[0]; got["model"] != "qwen2.5:7b" || got["stream"] != false ||
		got["options"].(map[string]any)["num_predict"] != float64(200) {
		t.Fatalf("asked %v", got)
	}
	schema := `{"type":"object","required":["tags"],"properties":{"tags":{"type":"array","items":{"type":"string"}}}}`
	ol.answers = []string{"not json", `{"tags":["fiqh"]}`}
	code, _, body = f.call(t, tok, "compute/generate", map[string]any{"prompt": "tags", "max_tokens": 50, "json_schema": schema})
	if code != 200 || !strings.Contains(string(body), "fiqh") {
		t.Fatalf("one retry = %d %s", code, body)
	}
	ol.answers = []string{`{"tags":"fiqh"}`}
	if code, c, _ := f.call(t, tok, "compute/generate", map[string]any{"prompt": "tags", "max_tokens": 50, "json_schema": schema}); code != 422 || c != CodeComputeBadOutput {
		t.Fatalf("bad output twice = %d %s", code, c)
	}
	if code, c, _ := f.call(t, tok, "compute/generate", map[string]any{"prompt": "x", "max_tokens": 1025}); code != 413 || c != CodeTooLarge {
		t.Fatalf("1,025 tokens = %d %s", code, c)
	}
	// no lease is left behind
	var n int
	_ = f.db.QueryRow(`SELECT count(*) FROM compute_leases`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d leases left", n)
	}
}

// T03: a model server on the internet is refused; this machine and the LAN are not.
func TestComputeRemoteRefused(t *testing.T) {
	f, _, ol, _ := computeFixture(t, "compute.llm")
	tok := f.mint(t).Token
	f.setRules(t, "plugin_compute", map[string]any{"url": "http://93.184.216.34:11434"})
	if code, c, _ := f.call(t, tok, "compute/generate", map[string]any{"prompt": "x", "max_tokens": 5}); code != 403 || c != CodeComputeRemote {
		t.Fatalf("internet address = %d %s", code, c)
	}
	// a name that resolves to the internet, too
	f.setRules(t, "plugin_compute", map[string]any{"url": "http://models.example:11434"})
	f.s.Compute.Resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.168.1.9"), netip.MustParseAddr("8.8.8.8")}, nil
	}
	if code, c, _ := f.call(t, tok, "compute/generate", map[string]any{"prompt": "x", "max_tokens": 5}); code != 403 || c != CodeComputeRemote {
		t.Fatalf("name with an internet address = %d %s", code, c)
	}
	if len(ol.asked) != 0 {
		t.Fatal("a refused question reached a model")
	}
	// the tag check's server is the default; a tag check server on the internet is refused too
	f.exec(t, `DELETE FROM app_settings WHERE key = 'rules.plugin_compute'`)
	f.setRules(t, "tagcheck", map[string]any{"url": "https://ollama.example.com"})
	f.s.Compute.Resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("34.1.2.3")}, nil
	}
	if code, c, _ := f.call(t, tok, "compute/generate", map[string]any{"prompt": "x", "max_tokens": 5}); code != 403 || c != CodeComputeRemote {
		t.Fatalf("tag check's internet server = %d %s", code, c)
	}
}

// T05: over the daily quota is plugin_quota; the next day counts again.
func TestComputeQuota(t *testing.T) {
	f, _, _, srv := computeFixture(t, "compute.llm", "compute.embed")
	tok := f.mint(t).Token
	day := time.Date(2026, 11, 6, 23, 0, 0, 0, time.UTC)
	f.s.Compute.Now = func() time.Time { return day }
	f.setRules(t, "plugin_compute", map[string]any{"url": srv.URL, "generate_per_day": 2, "embed_texts_per_day": 5})
	for i := 0; i < 2; i++ {
		if code, c, _ := f.call(t, tok, "compute/generate", map[string]any{"prompt": "x", "max_tokens": 5}); code != 200 {
			t.Fatalf("call %d = %d %s", i, code, c)
		}
	}
	if code, c, _ := f.call(t, tok, "compute/generate", map[string]any{"prompt": "x", "max_tokens": 5}); code != 403 || c != CodeQuota {
		t.Fatalf("third call = %d %s", code, c)
	}
	if code, _, _ := f.call(t, tok, "compute/embed", map[string]any{"texts": []string{"a", "b", "c", "d"}}); code != 200 {
		t.Fatal("4 of 5 texts")
	}
	if code, c, _ := f.call(t, tok, "compute/embed", map[string]any{"texts": []string{"a", "b"}}); code != 403 || c != CodeQuota {
		t.Fatalf("6 of 5 texts = %d %s", code, c)
	}
	day = day.Add(2 * time.Hour)
	if code, c, _ := f.call(t, tok, "compute/generate", map[string]any{"prompt": "x", "max_tokens": 5}); code != 200 {
		t.Fatalf("next day = %d %s", code, c)
	}
}

// T06: prompts and answers are never logged, only sizes and timings.
func TestComputeLogsNoText(t *testing.T) {
	f, _, ol, _ := computeFixture(t, "compute.llm", "compute.embed")
	var buf bytes.Buffer
	f.s.Logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ol.answers = []string{"SECRET-ANSWER"}
	tok := f.mint(t).Token
	_, _, _ = f.call(t, tok, "compute/generate", map[string]any{"prompt": "SECRET-PROMPT", "max_tokens": 5})
	_, _, _ = f.call(t, tok, "compute/embed", map[string]any{"texts": []string{"SECRET-TEXT"}})
	if s := buf.String(); strings.Contains(s, "SECRET") || !strings.Contains(s, "prompt_bytes") {
		t.Fatalf("log: %s", s)
	}
}

// T04: with core lookup work queued, a plugin flooding questions gets at most 1 in 3 slots;
// with none queued it gets every free one, and a running core job holds the slot.
func TestComputeLookupShare(t *testing.T) {
	f, _, _, _ := computeFixture(t, "compute.llm")
	ctx := context.Background()
	f.exec(t, `UPDATE worker_pools SET size = 1 WHERE pool = 'lookup'`)
	f.exec(t, `UPDATE job_stages SET enabled = true, paused = false WHERE stage = 'check_tags'`)
	core := func() bool { // a core claimer's turn, as engine/claim.py takes it
		tx, _ := f.db.Begin()
		defer func() { _ = tx.Rollback() }()
		_, _ = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('maktaba.pool:lookup'))`)
		var busy int
		_ = tx.QueryRow(`SELECT (SELECT count(*) FROM processing_jobs b JOIN job_stages s ON s.stage=b.stage
			WHERE s.pool='lookup' AND b.state IN ('claimed','running'))
			+ (SELECT count(*) FROM compute_leases WHERE pool='lookup' AND expires_at > now())`).Scan(&busy)
		if busy >= 1 {
			return false
		}
		_, _ = tx.Exec(`INSERT INTO claim_shares (owner, pass) VALUES ('plugin_share:lookup', 1)
			ON CONFLICT (owner) DO UPDATE SET pass = claim_shares.pass + 1`)
		return tx.Commit() == nil
	}
	plugin := func() bool {
		id, ok, err := tryLease(ctx, f.db, testPlugin, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			endLease(f.db, id)
		}
		return ok
	}
	// no core work waiting: the plugin takes every free slot
	for i := 0; i < 5; i++ {
		if !plugin() {
			t.Fatalf("slot %d refused with no core work waiting", i)
		}
	}
	// 20 core jobs queued: each slot goes to the plugin if it may, else to core
	for i := 0; i < 20; i++ {
		f.exec(t, `INSERT INTO processing_jobs (stage, state, subject_kind, subject_id) VALUES ('check_tags','pending','season',$1)`,
			"00000000-0000-0000-0000-0000000000"+string(rune('a'+i/10))+string(rune('0'+i%10)))
	}
	plugins, cores := 0, 0
	for i := 0; i < 30; i++ {
		switch {
		case plugin():
			plugins++
		case core():
			cores++
		}
	}
	if cores < 2*plugins || plugins == 0 {
		t.Fatalf("core %d, plugin %d of 30 slots", cores, plugins)
	}
	// a running core job holds the only slot
	f.exec(t, `UPDATE processing_jobs SET state = 'running' WHERE id = (SELECT min(id) FROM processing_jobs WHERE stage = 'check_tags')`)
	f.exec(t, `UPDATE claim_shares SET pass = 5 WHERE owner = 'plugin_share:lookup'`)
	if plugin() {
		t.Fatal("a slot taken while core holds it")
	}
	// a question waiting for a slot gives up when its time ends
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, ref := f.s.Compute.takeLease(short, f.db, testPlugin, time.Minute); ref == nil || ref.Code != CodeComputeUnavailable {
		t.Fatalf("wait past its time = %+v", ref)
	}
}
