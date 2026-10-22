package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

func fixedKeyring(key string, at time.Time) *Keyring {
	k := NewKeyring([]byte(key))
	k.now = func() time.Time { return at }
	return k
}

// TC3 of 32.3: good, expired, tampered path, wrong key; the query form for segments.
func TestSignedRequestVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	k := fixedKeyring("k1", now)
	sig := k.Sign("GET", "/stream/channel/x/live.ts", SignTTL)
	if err := k.Check(sig, "GET", "/stream/channel/x/live.ts"); err != nil {
		t.Fatalf("good signature refused: %v", err)
	}
	if err := k.Check(sig, "GET", "/stream/channel/y/live.ts"); !errors.Is(err, ErrBadSignature) {
		t.Error("tampered path accepted")
	}
	if err := k.Check(sig, "POST", "/stream/channel/x/live.ts"); err == nil {
		t.Error("other method accepted")
	}
	later := fixedKeyring("k1", now.Add(2*time.Minute))
	if err := later.Check(sig, "GET", "/stream/channel/x/live.ts"); err == nil {
		t.Error("expired signature accepted")
	}
	other := fixedKeyring("k2", now)
	if err := other.Check(sig, "GET", "/stream/channel/x/live.ts"); err == nil {
		t.Error("wrong key accepted")
	}
	// rotation: the previous key stays valid for a moment
	k.Set([]byte("k3"))
	if err := k.Check(sig, "GET", "/stream/channel/x/live.ts"); err != nil {
		t.Errorf("previous key refused within the grace: %v", err)
	}
	k.now = func() time.Time { return now.Add(30 * time.Second) }
	if err := k.Check(sig, "GET", "/stream/channel/x/live.ts"); err == nil {
		t.Error("previous key accepted after the grace")
	}

	// middleware: header everywhere, query only for segments
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mw := VerifyMiddleware(fixedKeyring("k1", now), nil)(ok)
	k1 := fixedKeyring("k1", now)
	for _, tc := range []struct {
		path   string
		query  bool
		header bool
		want   int
	}{
		{"/stream/channel/a/index.m3u8", false, false, 403},
		{"/stream/channel/a/index.m3u8", false, true, 200},
		{"/stream/channel/a/seg-5.ts", true, false, 200},
		{"/stream/channel/a/index.m3u8", true, false, 403},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		if tc.query {
			r = httptest.NewRequest("GET", tc.path+"?"+k1.SignedQuery(tc.path, 9), nil)
			if g := PlacementGen(r); g != 9 {
				t.Errorf("query generation = %d", g)
			}
		}
		if tc.header {
			k1.SignRequest(r, 9)
		}
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, r)
		if rec.Code != tc.want {
			t.Errorf("%s header=%v query=%v: %d, want %d", tc.path, tc.header, tc.query, rec.Code, tc.want)
		}
	}
}

// TC3 of 32.1: NVENC capacity is half the session cap; the override wins.
func TestCapacityNVENCCap(t *testing.T) {
	out := channel.OutputSettings{NVENCMax: 6}
	if c := Capacity(out, channel.EncoderNVENC, nil); c != 3 {
		t.Errorf("nvenc_max 6 -> %d, want 3", c)
	}
	two := 2
	if c := Capacity(out, channel.EncoderNVENC, &two); c != 2 {
		t.Errorf("override 2 -> %d", c)
	}
	if c := Capacity(out, channel.EncoderVideoToolbox, nil); c != 16 {
		t.Errorf("videotoolbox -> %d, want 16", c)
	}
	if c := Capacity(out, channel.EncoderSoftware, nil); c != 8 {
		t.Errorf("software -> %d, want 8", c)
	}
	if c := Capacity(channel.OutputSettings{NVENCMax: 1}, channel.EncoderNVENC, nil); c != 1 {
		t.Errorf("nvenc_max 1 -> %d, want 1", c)
	}
}

// TC5 of 32.1.
func TestCanPushDetection(t *testing.T) {
	with := "Supported file protocols:\nInput:\n  file\n  rtmps\n  srt\nOutput:\n  file\n  rtmp\n  rtmps\n  srt\n"
	got := ParseProtocols(with)
	if !got["rtmps"] || !got["srt"] {
		t.Errorf("with: %v", got)
	}
	without := "Supported file protocols:\nInput:\n  rtmps\nOutput:\n  file\n  rtmp\n"
	got = ParseProtocols(without)
	if got["rtmps"] || got["srt"] {
		t.Errorf("without: %v (input-only protocols do not push)", got)
	}
}

// TC1 of 32.1: node mode from a temp node config (DSN, node id); the cluster's path
// map JSON is what the station maps through.
func TestNodeModeConfigFromNodeJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node.json")
	cfg := map[string]string{"node_id": "6f1c9a2e-1111-4000-8000-000000000001", "name": "PC",
		"private_key": "x", "database_url": "postgresql://node_6f1c9a2e@home/maktaba"}
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"MAKTABA_NODE_CONFIG": p}
	getenv := func(k string) string { return env[k] }
	ns, ok := NodeMode(getenv, "windows", dir, os.ReadFile)
	if !ok || ns.NodeID != cfg["node_id"] || ns.DatabaseURL != cfg["database_url"] {
		t.Fatalf("node mode = %+v %v", ns, ok)
	}
	env["MAKTABA_NODE_ID"], env["DATABASE_URL"] = "n2", "postgresql://other"
	if ns, _ := NodeMode(getenv, "windows", dir, os.ReadFile); ns.NodeID != "n2" || ns.DatabaseURL != "postgresql://other" {
		t.Errorf("overrides: %+v", ns)
	}
	// no node config: the home
	empty := func(string) string { return "" }
	if _, ok := NodeMode(empty, "linux", t.TempDir(), os.ReadFile); ok {
		t.Error("a machine without a node config is not a node")
	}
	// the roaming node.json under its backup name (a claim's ReplaceFileW failed with
	// 1177, and putting it back too): still this node, with its database
	roaming := filepath.Join(dir, "Roaming", "Maktaba")
	if err := os.MkdirAll(roaming, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roaming, ".node.json-1792600000-4242-0badf00d.bak"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	winEnv := map[string]string{"APPDATA": filepath.Join(dir, "Roaming"), "LOCALAPPDATA": filepath.Join(dir, "Local"),
		"MAKTABA_MACHINE_ID": "this-pc"}
	ns, ok = NodeMode(func(k string) string { return winEnv[k] }, "windows", dir, os.ReadFile)
	if !ok || ns.NodeID != cfg["node_id"] || ns.DatabaseURL != cfg["database_url"] ||
		ns.ConfigPath != filepath.Join(roaming, "node.json") {
		t.Fatalf("node mode from the roaming config's backup = %+v %v", ns, ok)
	}
	// a node.json saved with a UTF-8 BOM (Notepad, PowerShell 5.1): read past it
	if err := os.WriteFile(p, append([]byte("\xef\xbb\xbf"), b...), 0o600); err != nil {
		t.Fatal(err)
	}
	delete(env, "MAKTABA_NODE_ID")
	delete(env, "DATABASE_URL")
	if ns, ok := NodeMode(getenv, "windows", dir, os.ReadFile); !ok || ns.NodeID != cfg["node_id"] || ns.DatabaseURL != cfg["database_url"] {
		t.Fatalf("node mode from a BOM'd node.json = %+v %v", ns, ok)
	}
	pm, err := pathmap.FromJSON([]byte(`{"Y:/": "/Volumes/Y/"}`))
	if err != nil || pm.Apply("Y:/Shows/a.mp4") != "/Volumes/Y/Shows/a.mp4" {
		t.Errorf("path map: %v %q", err, pm.Apply("Y:/Shows/a.mp4"))
	}
}

type fakeBeats struct {
	mu    sync.Mutex
	infos []BeatInfo
	ans   BeatAnswer
}

func (f *fakeBeats) Beat(_ context.Context, _ string, info BeatInfo) (BeatAnswer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infos = append(f.infos, info)
	return f.ans, nil
}

func TestHostLoopInstallsKeyAndAnswer(t *testing.T) {
	two := 2
	fb := &fakeBeats{ans: BeatAnswer{Enabled: true, CapacityOverride: &two, KeyB64: "a2V5LTE="}}
	k := NewKeyring(nil)
	l := &HostLoop{Store: fb, HostID: "h", Keyring: k,
		Info: func(context.Context) BeatInfo { return BeatInfo{Role: "home", Capacity: 16} }}
	if err := l.BeatOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !k.Ready() {
		t.Error("stream key not installed")
	}
	a, ok := l.Answer()
	if !ok || a.CapacityOverride == nil || *a.CapacityOverride != 2 {
		t.Errorf("answer %+v", a)
	}
	if len(fb.infos) != 1 || fb.infos[0].Capacity != 16 {
		t.Errorf("beats %+v", fb.infos)
	}
}

type looks struct{ l channel.Look }

func (x looks) Look(context.Context, uuid.UUID) (channel.Look, error) { return x.l, nil }

// TC7 of 32.1: art fetched once, fetched again on a hash change.
func TestAssetCacheFetchAndRefresh(t *testing.T) {
	homeDir := t.TempDir()
	logo := filepath.Join(homeDir, "logo.png")
	_ = os.WriteFile(logo, []byte("one"), 0o644)
	k := NewKeyring([]byte("secret"))
	var hits, fulls atomic.Int32
	h := &AssetHandler{Looks: looks{channel.Look{LogoPath: logo}}, Keyring: k}
	r := chi.NewRouter()
	h.Mount(r)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code == 200 {
			fulls.Add(1)
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	defer srv.Close()
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	c := &AssetCache{Base: func() string { return srv.URL }, Keyring: k, Dir: t.TempDir(), Now: clock}
	ch := uuid.New()
	p1 := c.Asset(context.Background(), ch, AssetLogo, "logo.png")
	if b, _ := os.ReadFile(p1); string(b) != "one" {
		t.Fatalf("first fetch: %q", b)
	}
	sum := sha256.Sum256([]byte("one"))
	if !strings.Contains(filepath.Base(p1), hex.EncodeToString(sum[:])) {
		t.Errorf("not cached by hash: %s", p1)
	}
	// within the TTL: no request
	c.Asset(context.Background(), ch, AssetLogo, "logo.png")
	if hits.Load() != 1 {
		t.Errorf("requests = %d, want 1", hits.Load())
	}
	// after the TTL, unchanged: a 304
	advance(2 * AssetTTL)
	if p := c.Asset(context.Background(), ch, AssetLogo, "logo.png"); p != p1 || fulls.Load() != 1 {
		t.Errorf("unchanged art refetched: %s full=%d", p, fulls.Load())
	}
	// changed on the home: fetched again
	_ = os.WriteFile(logo, []byte("two!"), 0o644)
	_ = os.Chtimes(logo, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	advance(2 * AssetTTL)
	p2 := c.Asset(context.Background(), ch, AssetLogo, "logo.png")
	if b, _ := os.ReadFile(p2); string(b) != "two!" || p2 == p1 {
		t.Errorf("changed art: %s %q", p2, b)
	}
	// unsigned requests are refused
	resp, err := http.Get(srv.URL + "/internal/tv/assets/" + ch.String() + "/logo")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("unsigned asset fetch: %d", resp.StatusCode)
	}
}

// TC2 of 32.1: a node answers 404 to HDHomeRun/guide routes, 403 unsigned, serves signed.
func TestNodeModeRoutes(t *testing.T) {
	k := NewKeyring([]byte("secret"))
	r := chi.NewRouter()
	eng := channel.NewEngine(nil, nil, nil, 1, nil)
	MountNode(r, k, &channel.HTTPHandler{Engine: eng}, func() Health { return Health{HostID: "n"} })
	srv := httptest.NewServer(r)
	defer srv.Close()
	for _, p := range []string{"/discover.json", "/lineup.json", "/xmltv.xml", "/lineup.m3u", "/auto/v1", "/device.xml"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s on a node: %d, want 404", p, resp.StatusCode)
		}
	}
	resp, err := http.Get(srv.URL + "/stream/channel/" + uuid.NewString() + "/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("unsigned playlist: %d, want 403", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+HealthPath, nil)
	k.SignRequest(req, 0)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var h Health
	_ = json.NewDecoder(resp.Body).Decode(&h)
	resp.Body.Close()
	if resp.StatusCode != 200 || h.HostID != "n" {
		t.Errorf("signed health: %d %+v", resp.StatusCode, h)
	}
}
