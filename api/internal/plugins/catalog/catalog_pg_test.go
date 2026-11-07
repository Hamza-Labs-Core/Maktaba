package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// Story 54.17 on Postgres with a fake CDN serving lists signed by TEST keys (made from public
// labels; no real build trusts them): the kill switch within one poll, warn and delist,
// rollbacks and mismatched heads refused, an expired list, updates and a bad download.

func signer(label string) pluginsig.Signer {
	seed := sha256.Sum256([]byte("maktaba-plugins TEST KEY, never for production: " + label))
	return pluginsig.NewSigner(seed[:])
}

type cdn struct {
	mu    sync.Mutex
	files map[string][]byte
	gets  []string
}

func (c *cdn) Get(_ context.Context, name string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets = append(c.gets, name)
	b, ok := c.files[name]
	if !ok {
		return nil, errors.New("404")
	}
	return b, nil
}

type kit struct {
	t     *testing.T
	db    *sql.DB
	root  pluginsig.Signer
	store pluginsig.Signer
	cdn   *cdn
	svc   *Service
	now   time.Time
	holds []string
	rev   int64
	cat   int64
}

func newKit(t *testing.T) *kit {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	k := &kit{t: t, db: db, root: signer("root-1"), store: signer("store-1"), cdn: &cdn{files: map[string][]byte{}},
		now: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	raw, err := pluginsig.SignEnvelope(pluginsig.KindRoot, pluginsig.RootSigned{Version: 1, Threshold: 1,
		Keys: []pluginsig.RootKey{{ID: k.root.ID, Public: k.root.Public()}}}, k.root)
	if err != nil {
		t.Fatal(err)
	}
	root, err := pluginsig.ParseRoot(raw)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := pluginsig.SignEnvelope(pluginsig.KindStoreKeys, pluginsig.StoreKeysSigned{Version: 1,
		Expires: pluginsig.FormatTime(k.now.Add(300 * 24 * time.Hour)), Keys: []pluginsig.StoreKey{{ID: k.store.ID,
			Public: k.store.Public(), Use: pluginsig.UseStore, NotBefore: pluginsig.FormatTime(k.now.Add(-30 * 24 * time.Hour)),
			NotAfter: pluginsig.FormatTime(k.now.Add(365 * 24 * time.Hour))}}}, k.root)
	k.cdn.files["store-keys.json"] = keys
	k.svc = &Service{DB: db, Store: k.cdn, StateDir: t.TempDir(), Root: root, Now: func() time.Time { return k.now },
		Hold: func(_ context.Context, id string, held bool, by string) error {
			if held {
				k.holds = append(k.holds, id+":"+by)
			}
			return nil
		}}
	return k
}

func (k *kit) install(id, version, devKey string) {
	k.t.Helper()
	if _, err := k.db.Exec(`INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest, review_level, developer_key_id)
		VALUES ($1, $2, $3, '/x', '{"capabilities":{"required":[],"optional":["tags.write"]}}', 'reviewed', $4)`, id, version, strings.Repeat("d", 64), devKey); err != nil {
		k.t.Fatal(err)
	}
}

// publish signs a revocation list (version k.rev+1) and its head.
func (k *kit) publish(entries ...pluginsig.RevocationEntry) []byte {
	k.t.Helper()
	k.rev++
	if entries == nil {
		entries = []pluginsig.RevocationEntry{}
	}
	raw, err := pluginsig.SignEnvelope(pluginsig.KindRevoked, pluginsig.RevokedSigned{Version: k.rev, Issued: pluginsig.FormatTime(k.now.Add(-time.Minute)),
		Expires: pluginsig.FormatTime(k.now.Add(47 * time.Hour)), Entries: entries}, k.store)
	if err != nil {
		k.t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	head, _ := pluginsig.SignEnvelope(pluginsig.KindRevokedHead, pluginsig.RevokedHeadSigned{Version: k.rev,
		Issued: pluginsig.FormatTime(k.now.Add(-time.Minute)), SHA256: hex.EncodeToString(sum[:])}, k.store)
	k.cdn.files["revoked.json"], k.cdn.files["revoked-head.json"] = raw, head
	return raw
}

func entry(id, versions, action string) pluginsig.RevocationEntry {
	return pluginsig.RevocationEntry{PluginID: id, Versions: versions, Action: action, ReasonCode: "malware", Issued: "2026-11-06T11:00:00Z"}
}

func (k *kit) revoked(id string) (bool, string) {
	k.t.Helper()
	var r bool
	var rev sql.NullString
	if err := k.db.QueryRow(`SELECT revoked, revocation::text FROM plugin_installs WHERE plugin_id = $1`, id).Scan(&r, &rev); err != nil {
		k.t.Fatal(err)
	}
	return r, rev.String
}

// T01, T02: a disable reaches a home within one head poll (5 minutes, the edge's 60 s, the
// jitter: under 7 minutes); the plugin is held and admins told; Start is refused. The home
// talks to the CDN only: no cloud host, no store link.
func TestDisableHoldsWithinOnePoll(t *testing.T) {
	k := newKit(t)
	ctx := context.Background()
	k.install("dev.example.p", "1.2.0", "")
	k.install("dev.example.fine", "2.0.0", "")
	k.publish()
	if err := k.svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	k.svc.Tick(ctx) // the first pass fetches the list
	if r, _ := k.revoked("dev.example.p"); r {
		t.Fatal("revoked before any entry")
	}
	k.publish(entry("dev.example.p", "<1.3.0", pluginsig.ActionDisable))
	published := k.now
	k.now = k.now.Add(HeadEvery + HeadJitter + time.Minute) // the next poll, the edge cache spent
	k.svc.Tick(ctx)
	if r, _ := k.revoked("dev.example.p"); !r {
		t.Fatal("not revoked after the next poll")
	}
	if k.now.Sub(published) > 7*time.Minute {
		t.Fatalf("took %s", k.now.Sub(published))
	}
	if len(k.holds) != 1 || k.holds[0] != "dev.example.p:"+HoldRevoked {
		t.Fatalf("holds %v", k.holds)
	}
	if r, _ := k.revoked("dev.example.fine"); r {
		t.Fatal("another plugin was revoked")
	}
	var n int
	_ = k.db.QueryRow(`SELECT count(*) FROM notifications WHERE title_key = 'notify.plugins.revoked' AND subject = 'dev.example.p'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d notices", n)
	}
	// the same list again: nothing more
	k.now = k.now.Add(HeadEvery)
	k.svc.Tick(ctx)
	if len(k.holds) != 1 {
		t.Fatalf("held again: %v", k.holds)
	}
	l := &plugins.Lifecycle{DB: k.db}
	for _, ch := range []svc.Change{{ID: "plugin.dev.example.p", Want: "enabled"}, {ID: "plugin.dev.example.p", Hold: new(bool)}} {
		err := l.Check(ctx, ch)
		var p *plugins.Problem
		if !errors.As(err, &p) || p.Code != CodeRevoked || p.Status != http.StatusConflict {
			t.Fatalf("start or enable a revoked plugin: %v", err)
		}
	}
	for _, g := range k.cdn.gets {
		if strings.Contains(g, "/") {
			t.Fatalf("a request for %s", g)
		}
	}
}

// T02: the HTTP store asks only the CDN's host, with nothing identifying but the version.
func TestOnlyTheCDNIsAsked(t *testing.T) {
	var hosts []string
	var agents []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		agents = append(agents, r.UserAgent()+"|"+r.Header.Get("Authorization")+"|"+r.Header.Get("Cookie"))
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	s := &HTTPStore{Base: srv.URL, Version: "0.7.0"}
	_, _ = s.Get(context.Background(), "revoked-head.json")
	u, _ := url.Parse(srv.URL)
	if len(hosts) != 1 || hosts[0] != u.Host || agents[0] != "Maktaba/0.7.0||" {
		t.Fatalf("hosts %v agents %v", hosts, agents)
	}
}

// T03: warn keeps it running with a warning; delist changes nothing installed; a lifted
// entry clears revoked (the hold stays for the admin) with a notice.
func TestWarnDelistAndLift(t *testing.T) {
	k := newKit(t)
	ctx := context.Background()
	k.install("dev.example.w", "1.0.0", "")
	k.install("dev.example.d", "1.0.0", "")
	k.install("dev.example.k", "1.0.0", "key-of-a-bad-dev")
	k.publish(entry("dev.example.w", "*", pluginsig.ActionWarn), entry("dev.example.d", "*", pluginsig.ActionDelist),
		pluginsig.RevocationEntry{PluginID: "dev.example.k", Versions: "*", DeveloperKeyID: "key-of-a-bad-dev", Action: pluginsig.ActionDisable, ReasonCode: "key_compromised", Issued: "2026-11-06T11:00:00Z"})
	if err := k.svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	k.svc.Tick(ctx)
	if r, rev := k.revoked("dev.example.w"); r || !strings.Contains(rev, `"warn"`) {
		t.Fatalf("warn: %v %s", r, rev)
	}
	if r, _ := k.revoked("dev.example.d"); r {
		t.Fatal("delist revoked an installed copy")
	}
	if r, _ := k.revoked("dev.example.k"); !r {
		t.Fatal("a disable by developer key did not apply")
	}
	k.publish(entry("dev.example.w", "*", pluginsig.ActionWarn))
	k.now = k.now.Add(HeadEvery)
	k.svc.Tick(ctx)
	if r, rev := k.revoked("dev.example.k"); r || rev != "" {
		t.Fatalf("lifted: %v %s", r, rev)
	}
	var n int
	_ = k.db.QueryRow(`SELECT count(*) FROM notifications WHERE title_key = 'notify.plugins.revocation_lifted'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d lifted notices", n)
	}
}

// T04 and the ACs: a head naming another list keeps the current one; an older list is
// refused and the newer kept.
func TestHeadMismatchAndRollbackAreRefused(t *testing.T) {
	k := newKit(t)
	ctx := context.Background()
	k.install("dev.example.p", "1.0.0", "")
	k.publish(entry("dev.example.p", "*", pluginsig.ActionDisable))
	_ = k.svc.Load(ctx)
	k.svc.Tick(ctx)
	newer := k.cdn.files["revoked.json"]
	// a new head, but the CDN still serves the old list under it
	k.publish()
	k.cdn.files["revoked.json"] = newer
	k.now = k.now.Add(HeadEvery)
	if err := k.svc.PollHead(ctx); err == nil {
		t.Fatal("a head naming another list was accepted")
	} else if code, _ := pluginsig.CodeOf(err); code != pluginsig.CodeRevocationHead {
		t.Fatalf("got %v", err)
	}
	if r, _ := k.revoked("dev.example.p"); !r {
		t.Fatal("the current list was dropped")
	}
	// an older list, validly signed
	old, _ := pluginsig.SignEnvelope(pluginsig.KindRevoked, pluginsig.RevokedSigned{Version: 1, Issued: pluginsig.FormatTime(k.now.Add(-time.Hour)),
		Expires: pluginsig.FormatTime(k.now.Add(40 * time.Hour)), Entries: []pluginsig.RevocationEntry{}}, k.store)
	k.cdn.files["revoked.json"] = old
	if err := k.svc.FetchRevoked(ctx, nil); err == nil {
		t.Fatal("an older list was accepted")
	} else if code, _ := pluginsig.CodeOf(err); code != pluginsig.CodeRevocationRollback {
		t.Fatalf("got %v", err)
	}
	if r, _ := k.revoked("dev.example.p"); !r {
		t.Fatal("the rollback lifted the revocation")
	}
	// the state survives a restart from the database even when the file is lost
	k.svc.StateDir = t.TempDir()
	if st := k.svc.State(ctx); st.RevokedVersion != 1 {
		t.Fatalf("state from Postgres: %+v", st)
	}
}

type installer struct {
	calls []string
	err   error
}

func (f *installer) Install(_ context.Context, req plugins.InstallRequest, _ svc.Actor) (*plugins.InstallResult, error) {
	f.calls = append(f.calls, req.ExpectID)
	return &plugins.InstallResult{PluginID: req.ExpectID}, f.err
}

func (k *kit) catalog(versions ...pluginsig.CatalogVersion) {
	k.t.Helper()
	k.cat++
	raw, err := pluginsig.SignEnvelope(pluginsig.KindCatalog, pluginsig.CatalogSigned{Version: k.cat, Issued: pluginsig.FormatTime(k.now.Add(-time.Minute)),
		Expires: pluginsig.FormatTime(k.now.Add(6 * 24 * time.Hour)), APIMajor: 1, Plugins: []pluginsig.CatalogPlugin{{
			ID: "dev.example.p", Developer: "Dev", Names: map[string]string{"en": "P", "ar": "ب"}, Summaries: map[string]string{"en": "", "ar": ""},
			Tags: []string{}, Price: "free", Machine: []string{}, Versions: versions}}}, k.store)
	if err != nil {
		k.t.Fatal(err)
	}
	k.cdn.files["catalog-1.json"] = raw
}

func (k *kit) version(v string, body []byte, caps ...string) pluginsig.CatalogVersion {
	sum := sha256.Sum256(body)
	h := hex.EncodeToString(sum[:])
	k.cdn.files[h+".mkplugin"] = body
	if caps == nil {
		caps = []string{"tags.write"}
	}
	return pluginsig.CatalogVersion{Version: v, SHA256: h, Size: int64(len(body)), URL: "https://plugins-cdn.maktaba.tv/" + h + ".mkplugin",
		Maktaba: ">=0.6.0, <0.9.0", PluginAPI: "1.0", Platforms: []string{"any"}, Capabilities: caps, ReviewLevel: "reviewed", Released: "2026-11-01T00:00:00Z"}
}

// T05: auto-update applies a version that adds nothing at night; one that adds a capability
// waits for the admin ("Needs your approval"); T07: a package whose sha256 is not the
// catalog's is refused and never installed.
func TestUpdatesAndDownloads(t *testing.T) {
	k := newKit(t)
	ctx := context.Background()
	k.install("dev.example.p", "1.0.0", "")
	k.publish()
	_ = k.svc.Load(ctx)
	k.svc.Tick(ctx)
	f := &installer{}
	k.svc.SetAuto(&Auto{L: f, Server: "0.7.0", Platform: "darwin-arm64"})
	k.catalog(k.version("1.0.0", []byte("one")), k.version("1.1.0", []byte("one-one"), "tags.write", "transcript.read"))
	if err := k.svc.FetchCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := k.db.Exec(`UPDATE plugin_installs SET auto_update = true`); err != nil {
		t.Fatal(err)
	}
	k.now = time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	k.svc.NightlyUpdates(ctx)
	if len(f.calls) != 0 || k.svc.NeedsApproval(ctx, "dev.example.p") != "1.1.0" {
		t.Fatalf("an update adding a capability: calls %v, needs %q", f.calls, k.svc.NeedsApproval(ctx, "dev.example.p"))
	}
	k.publish() // a fresh list for the night
	_ = k.svc.FetchRevoked(ctx, nil)
	k.catalog(k.version("1.0.0", []byte("one")), k.version("1.2.0", []byte("one-two")))
	if err := k.svc.FetchCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	k.svc.NightlyUpdates(ctx)
	if len(f.calls) != 1 {
		t.Fatalf("a plain update at night: %v", f.calls)
	}
	// by day nothing applies
	k.now = time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC)
	k.svc.NightlyUpdates(ctx)
	if len(f.calls) != 1 {
		t.Fatalf("by day: %v", f.calls)
	}
	// a CDN serving the wrong bytes under a sha256
	v := k.version("1.3.0", []byte("good"))
	k.catalog(v)
	_ = k.svc.FetchCatalog(ctx)
	k.cdn.files[v.SHA256+".mkplugin"] = []byte("evil")
	_, err := k.svc.InstallFromStore(ctx, f, "dev.example.p", "1.3.0", nil, svc.Actor{By: "owner"})
	var p *Problem
	if !errors.As(err, &p) || p.Code != "package_digest_mismatch" || len(f.calls) != 1 {
		t.Fatalf("a bad download: %v (calls %v)", err, f.calls)
	}
}

// The ACs on an expired list: plugins keep running, the warning shows, store installs wait.
func TestAnExpiredListWarnsAndStoreInstallsWait(t *testing.T) {
	k := newKit(t)
	ctx := context.Background()
	k.install("dev.example.p", "1.0.0", "")
	k.publish()
	_ = k.svc.Load(ctx)
	k.svc.Tick(ctx)
	k.catalog(k.version("1.1.0", []byte("x")))
	_ = k.svc.FetchCatalog(ctx)
	k.now = k.now.Add(5 * 24 * time.Hour) // the list expired 3 days ago; no network
	k.cdn.files = map[string][]byte{}
	k.svc.Tick(ctx)
	tr := k.svc.Trust(ctx)
	if !tr.RevokedExpired || tr.StoreInstallsOK {
		t.Fatalf("trust %+v", tr)
	}
	if r, _ := k.revoked("dev.example.p"); r {
		t.Fatal("an expired list stopped a plugin")
	}
	_, err := k.svc.InstallFromStore(ctx, &installer{}, "dev.example.p", "1.1.0", nil, svc.Actor{By: "owner"})
	var p *Problem
	if !errors.As(err, &p) || p.Code != CodeListExpired {
		t.Fatalf("a store install with an expired list: %v", err)
	}
}

func TestMatchVersions(t *testing.T) {
	for _, c := range []struct {
		rng, v string
		want   bool
	}{{"*", "9.9.9", true}, {"<1.3.0", "1.2.0", true}, {"<1.3.0", "1.3.0", false}, {">=1.0.0, <2.0.0", "1.5.1", true}, {"1.2.0", "1.2.0", true}, {"1.2.0", "1.2.1", false}} {
		if MatchVersions(c.rng, c.v) != c.want {
			t.Errorf("%s %s", c.rng, c.v)
		}
	}
}

// 54.15 §10: a linked home reports its installed plugins once a day (the hook is nil for an
// unlinked home or with telemetry off).
func TestSeenOnceADay(t *testing.T) {
	k := newKit(t)
	ctx := context.Background()
	k.install("dev.example.p", "1.0.0", "")
	k.publish()
	var sent [][]SeenPlugin
	k.svc.Seen = func(_ context.Context, l []SeenPlugin) error { sent = append(sent, l); return nil }
	_ = k.svc.Load(ctx)
	k.svc.Tick(ctx)
	k.now = k.now.Add(HeadEvery)
	k.svc.Tick(ctx)
	if len(sent) != 1 || len(sent[0]) != 1 || sent[0][0].ID != "dev.example.p" {
		t.Fatalf("sent %v", sent)
	}
	k.now = k.now.Add(SeenEvery)
	k.svc.Tick(ctx)
	if len(sent) != 2 {
		t.Fatalf("sent %d times", len(sent))
	}
}
