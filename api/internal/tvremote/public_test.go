package tvremote_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

// Epic 34, first slice: public channels (34.1), always-on pushes (34.3) and
// the state the home posts to the site's Worker (34.2, 34.4).

type pubFx struct {
	*remoteFx
	audits []string
}

func newPub(t *testing.T) *pubFx {
	t.Helper()
	fx := &pubFx{remoteFx: newRemote(t)}
	fx.setting(tvremote.SettingLastTest, map[string]any{"ok": true, "checked_at": fx.clock(), "lines": []any{}})
	fx.c.Audit = func(_ context.Context, action, actor, target string, _ map[string]any) {
		fx.audits = append(fx.audits, action+" "+actor+" "+target)
	}
	return fx
}

func ptr[T any](v T) *T { return &v }

func (fx *pubFx) set(ch string, c tvremote.PublicChange) (tvremote.PublicChannel, *tvremote.TuneError) {
	fx.t.Helper()
	x, err := fx.c.SetPublic(context.Background(), "admin-1", ch, c)
	if err != nil {
		var te *tvremote.TuneError
		if errors.As(err, &te) {
			return x, te
		}
		fx.t.Fatal(err)
	}
	return x, nil
}

func (fx *pubFx) makePublic(ch string) {
	fx.t.Helper()
	if _, te := fx.set(ch, tvremote.PublicChange{Public: ptr(true), RightsConfirmed: true}); te != nil {
		fx.t.Fatal(te)
	}
}

// 34.1 AC1, AC2: public needs the rights confirmation, each time; going
// private keeps who confirmed and when, for audit.
func TestPublicRequiresRights(t *testing.T) {
	fx := newPub(t)
	ch := fx.chans[0]
	if _, te := fx.set(ch, tvremote.PublicChange{Public: ptr(true)}); te == nil || te.Status != http.StatusUnprocessableEntity ||
		te.Code != "rights_confirmation_required" {
		t.Fatalf("public without the confirmation: %v", te)
	}
	x, te := fx.set(ch, tvremote.PublicChange{Public: ptr(true), RightsConfirmed: true, Note: ptr("our own recordings")})
	if te != nil || !x.Public || x.RightsConfirmedBy == nil || *x.RightsConfirmedBy != "admin-1" || x.RightsConfirmedAt == nil {
		t.Fatalf("public: %+v %v", x, te)
	}
	if x, _ = fx.set(ch, tvremote.PublicChange{Public: ptr(false)}); x.Public || x.RightsConfirmedBy == nil || x.RightsNote != "our own recordings" {
		t.Fatalf("private must keep the confirmation for audit: %+v", x)
	}
	if _, te := fx.set(ch, tvremote.PublicChange{Public: ptr(true)}); te == nil || te.Code != "rights_confirmation_required" {
		t.Fatalf("public again without a new confirmation: %v", te)
	}
	if len(fx.audits) != 2 || fx.audits[0] != "tv_public.public admin-1 "+ch || fx.audits[1] != "tv_public.private admin-1 "+ch {
		t.Errorf("audit %v", fx.audits)
	}
}

func TestPublicSlug(t *testing.T) {
	fx := newPub(t)
	if x, _ := fx.set(fx.chans[0], tvremote.PublicChange{Slug: ptr("news")}); x.Slug != "news" {
		t.Fatalf("slug %+v", x)
	}
	fx.makePublic(fx.chans[0])
	for _, bad := range []string{"", "News", "-x", "a b", "x/y"} {
		if _, te := fx.set(fx.chans[1], tvremote.PublicChange{Slug: ptr(bad)}); te == nil || te.Code != "invalid_slug" {
			t.Errorf("slug %q: %v", bad, te)
		}
	}
	if _, te := fx.set(fx.chans[1], tvremote.PublicChange{Slug: ptr("news")}); te == nil || te.Status != http.StatusConflict {
		t.Errorf("a taken slug: %v", te)
	}
	if _, te := fx.set("nope", tvremote.PublicChange{}); te == nil || te.Status != http.StatusNotFound {
		t.Errorf("no channel: %v", te)
	}
	if got := tvremote.SlugOf("Al Jazeera  Docs!"); got != "al-jazeera-docs" {
		t.Errorf("SlugOf = %q", got)
	}
}

// 34.3 AC2: a public channel is pushed always on, with nobody watching; it
// stops like any idle push once it turns private.
func TestPublicPushAlwaysOn(t *testing.T) {
	fx := newPub(t)
	ch := fx.chans[0]
	fx.makePublic(ch)
	fx.reconcile()
	if ps := fx.pushes(ch); len(ps) != 0 {
		t.Fatalf("pushed while the site is off: %+v", ps)
	}
	fx.setting(tvremote.PublicSettingsKey, map[string]any{"enabled": true, "site_url": "https://tv.example.com"})
	fx.reconcile()
	if p := fx.last(ch); p.State != tvremote.PushRequested {
		t.Fatalf("no push for the public channel: %+v", p)
	}
	if ps := fx.pushes(fx.chans[1]); len(ps) != 0 {
		t.Fatalf("a private channel was pushed: %+v", ps)
	}
	p := fx.goLive(ch)
	fx.advance(30 * time.Minute) // far past the 3-minute idle timeout
	fx.reconcile()
	fx.reconcile()
	if q := fx.last(ch); q.ID != p.ID || q.State != tvremote.PushLive {
		t.Fatalf("the public push did not stay up: %+v", q)
	}
	var holds string
	_ = fx.db.QueryRow(`SELECT holds FROM tv_remote_pushes WHERE id = $1`, p.ID).Scan(&holds)
	if holds != `["public"]` {
		t.Errorf("holds %s", holds)
	}
	if _, te := fx.set(ch, tvremote.PublicChange{Public: ptr(false)}); te != nil {
		t.Fatal(te)
	}
	fx.reconcile()
	fx.advance(4 * time.Minute)
	fx.reconcile()
	fx.reconcile()
	if q := fx.last(ch); q.State == tvremote.PushLive {
		t.Fatalf("the push kept going after the channel turned private: %+v", q)
	}
}

// 32.10: a used-up budget (block_new) stops new public pushes too.
func TestPublicPushWaitsForBudget(t *testing.T) {
	fx := newPub(t)
	fx.makePublic(fx.chans[0])
	fx.setting(tvremote.PublicSettingsKey, map[string]any{"enabled": true})
	fx.rules(map[string]any{"monthly_budget_usd": 5}) // the storage plan alone is $5
	fx.reconcile()
	if ps := fx.pushes(fx.chans[0]); len(ps) != 0 {
		t.Fatalf("a public push started with the budget used up: %+v", ps)
	}
	fx.rules(map[string]any{})
	fx.reconcile()
	if p := fx.last(fx.chans[0]); p.State != tvremote.PushRequested {
		t.Fatalf("no push once the budget allows: %+v", p)
	}
}

// Two posts in the same second (a wake right after a tick) carry strictly
// increasing times: the Worker refuses a time it has seen.
func TestPublisherTimesIncrease(t *testing.T) {
	fx := newPub(t)
	var mu sync.Mutex
	var times []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts, _ := strconv.ParseInt(r.Header.Get(tvremote.HeaderHomeTime), 10, 64)
		mu.Lock()
		times = append(times, ts)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	fx.setting(tvremote.PublicSettingsKey, map[string]any{"enabled": true, "site_url": srv.URL})
	_ = fx.store.Put(context.Background(), tvremote.SecretSiteHMAC, []byte("s"), "")
	pub := &tvremote.Publisher{Ctl: fx.c} // the fixture's clock does not move
	for i := 0; i < 3; i++ {
		if err := pub.PublishOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) != 3 || times[1] <= times[0] || times[2] <= times[1] || times[0] != fx.clock().UnixMilli() {
		t.Fatalf("times %v", times)
	}
}

// 34.2 / 34.4: the home posts the public lineup and each live channel's
// video id, signed; private channels are not in it.
func TestPublisherPostsSignedState(t *testing.T) {
	fx := newPub(t)
	fx.makePublic(fx.chans[0])
	fx.setting(tvremote.PublicSettingsKey, map[string]any{"enabled": true, "site_url": "https://tv.example.com"})
	fx.reconcile()
	p := fx.goLive(fx.chans[0])
	var mu sync.Mutex
	var got tvremote.SiteState
	var sigOK bool
	secret := []byte("0123456789abcdef0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get(tvremote.HeaderHomeTime), 10, 64)
		mu.Lock()
		defer mu.Unlock()
		sigOK = r.URL.Path == tvremote.SiteStatePath &&
			r.Header.Get(tvremote.HeaderHomeSignature) == tvremote.SignHome(secret, r.Method, r.URL.Path, ts, body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	// the test server is http on 127.0.0.1: allowed (wrangler dev)
	fx.setting(tvremote.PublicSettingsKey, map[string]any{"enabled": true, "site_url": srv.URL})
	pub := &tvremote.Publisher{Ctl: fx.c}
	if err := pub.PublishOnce(context.Background()); !errors.Is(err, tvremote.ErrSiteNotSet) {
		t.Fatalf("published without secrets: %v", err)
	}
	_ = fx.store.Put(context.Background(), tvremote.SecretSiteHMAC, secret, "")
	if err := pub.PublishOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sigOK {
		t.Fatal("the post's signature does not verify")
	}
	if got.CustomerCode != "abc123xyz" || len(got.Channels) != 1 || !got.Channels[0].OnAir || got.Channels[0].VideoUID != p.Video {
		t.Fatalf("state %+v", got)
	}
	if st := pub.Status(); st.LastOKAt == nil || st.LastError != "" {
		t.Errorf("status %+v", st)
	}
}

// The site's own signing key: created at Cloudflare, the previous one
// deleted there; removing the integration deletes it with the secrets.
func TestSiteSecrets(t *testing.T) {
	fx := newPub(t)
	ctx := context.Background()
	a, err := fx.c.NewSiteSecrets(ctx, "admin-1")
	if err != nil || a.SigningKeyID == "" || a.SigningKeyJWK == "" || len(a.HomeHMAC) != 64 {
		t.Fatalf("secrets %+v %v", a, err)
	}
	b, err := fx.c.NewSiteSecrets(ctx, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fx.f.Keys[a.SigningKeyID]; ok {
		t.Error("the previous site key was kept at Cloudflare")
	}
	if _, ok := fx.f.Keys[b.SigningKeyID]; !ok || b.HomeHMAC == a.HomeHMAC {
		t.Error("the new site key is missing, or the HMAC secret was not renewed")
	}
	fx.in.StopPushes, fx.in.ForgetInputs = fx.c.StopAll, fx.c.Forget
	if _, err := fx.in.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := fx.f.Keys[b.SigningKeyID]; ok {
		t.Error("the site key survived the integration's removal")
	}
	for _, n := range []string{tvremote.SecretSiteSigningKey, tvremote.SecretSiteHMAC} {
		if ok, _ := fx.store.Has(ctx, n); ok {
			t.Errorf("%s kept", n)
		}
	}
}

func TestValidSiteURL(t *testing.T) {
	for s, ok := range map[string]bool{"": true, "https://tv.example.com": true, "http://127.0.0.1:8787": true,
		"http://localhost:8787": true, "http://tv.example.com": false, "https://tv.example.com/x": false,
		"https://u:p@tv.example.com": false, "ftp://x": false, "https://tv.example.com?a=1": false} {
		if err := tvremote.ValidSiteURL(s); (err == nil) != ok {
			t.Errorf("%q: %v", s, err)
		}
	}
}
