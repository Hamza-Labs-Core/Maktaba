package tvremote_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
)

func cfClaims(tok string) (cf.TokenClaims, bool) { return cftest.Claims(tok) }

// liveTune tunes and brings the push live, then tunes again for the URL.
func (fx *remoteFx) liveTune(user, ch string) tvremote.Tuned {
	fx.t.Helper()
	if _, te := fx.tune(user, ch, "phone"); te != nil {
		fx.t.Fatal(te)
	}
	if p := fx.last(ch); p.State != tvremote.PushLive {
		fx.goLive(ch)
	}
	r, te := fx.tune(user, ch, "phone")
	if te != nil || r.State != "live" || r.HLSURL == "" {
		fx.t.Fatalf("tune: %+v %v", r, te)
	}
	return r
}

func tokenOf(url string) string {
	// …/customer/<token>/manifest/video.m3u8
	parts := strings.Split(url, "/")
	for i, p := range parts {
		if p == "manifest" && i > 0 {
			return parts[i-1]
		}
	}
	return ""
}

// TC1: one test per refusal, in the gates' order.
func TestTuneGatesOrder(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(fx *remoteFx)
		user   string
		status int
		code   string
	}{
		{"not set up", func(fx *remoteFx) { fx.ext.settings = map[string]any{} }, "u", 503, "remote_unavailable"},
		{"remote off", func(fx *remoteFx) { fx.setting(tvremote.SettingEnabled, false) }, "u", 503, "remote_unavailable"},
		{"admins only", func(fx *remoteFx) { fx.rules(map[string]any{"viewers": "admins"}) }, "u", 403, "remote_not_allowed"},
		{"budget used up", func(fx *remoteFx) {
			fx.rules(map[string]any{"monthly_budget_usd": 4}) // the storage block alone is $5
		}, "u", 402, "budget_exhausted"},
		{"two streams already", func(fx *remoteFx) {
			_, _ = fx.tune("u", fx.chans[1], "a")
			_, _ = fx.tune("u", fx.chans[2], "b")
		}, "u", 429, "user_streams_full"},
		{"every remote channel in use", func(fx *remoteFx) {
			fx.rules(map[string]any{"max_channels": 1})
			_, _ = fx.tune("other", fx.chans[1], "")
		}, "u", 429, "remote_channels_full"},
		{"storage full", func(fx *remoteFx) {
			fx.f.StorageLimit = 900 // one 8 h broadcast (480) + a margin fits, two do not
			_, _ = fx.tune("other", fx.chans[1], "")
		}, "u", 503, "remote_storage_full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newRemote(t)
			tc.setup(fx)
			_, te := fx.tune(tc.user, fx.chans[0], "phone")
			if te == nil || te.Status != tc.status || te.Code != tc.code {
				t.Fatalf("got %+v, want %d %s", te, tc.status, tc.code)
			}
			if tc.code == "remote_storage_full" && !strings.Contains(te.Hint, "1,000-minute block") {
				t.Errorf("hint %q", te.Hint)
			}
		})
	}
	t.Run("unknown or unreadable channel", func(t *testing.T) {
		fx := newRemote(t)
		v := tvremote.Viewer{UserID: "u", CanRead: func(*string) bool { return false }}
		lib := "6f1c9a2e-0000-4000-8000-0000000000aa"
		fx.exec(`UPDATE channels SET library_id = $1 WHERE id = $2`, lib, fx.chans[0])
		_, err := fx.c.Tune(context.Background(), v, fx.chans[0], "")
		if te, ok := err.(*tvremote.TuneError); !ok || te.Status != http.StatusNotFound {
			t.Fatalf("%v", err)
		}
	})
}

// TC2: two users on one channel → one push, two sessions.
func TestTuneReusesPush(t *testing.T) {
	fx := newRemote(t)
	a := fx.liveTune("ua", fx.chans[0])
	b, te := fx.tune("ub", fx.chans[0], "tv")
	if te != nil || b.State != "live" {
		t.Fatalf("second user: %+v %v", b, te)
	}
	if a.SessionID == b.SessionID || len(fx.pushes(fx.chans[0])) != 1 || len(fx.f.Inputs) != 1 {
		t.Fatalf("pushes %d inputs %d", len(fx.pushes(fx.chans[0])), len(fx.f.Inputs))
	}
}

// TC3: the token is RS256 with the integration's key; its claims.
func TestTokenClaims(t *testing.T) {
	fx := newRemote(t)
	fx.rules(map[string]any{"allowed_countries": []string{"sa", "EG"}, "token_ttl_minutes": 30})
	r := fx.liveTune("u", fx.chans[0])
	if !strings.HasPrefix(r.HLSURL, fx.f.URL+"/customer/") || !strings.HasSuffix(r.HLSURL, "/manifest/video.m3u8") {
		t.Fatalf("url %s", r.HLSURL)
	}
	tok := tokenOf(r.HLSURL)
	pub, _ := fx.f.PublicKey(fx.kid)
	if err := cftest.VerifyToken(tok, pub); err != nil {
		t.Fatalf("the token does not verify with the signing key: %v", err)
	}
	c, _ := cfClaims(tok)
	now := fx.clock().Unix()
	p := fx.last(fx.chans[0])
	if c.Sub != p.Video || c.Kid != fx.kid || c.Exp != now+30*60 || c.Nbf != now-30 {
		t.Errorf("claims %+v (now %d, video %s)", c, now, p.Video)
	}
	if len(c.AccessRules) != 2 || c.AccessRules[0].Type != "ip.geoip.country" || strings.Join(c.AccessRules[0].Country, ",") != "SA,EG" ||
		c.AccessRules[1].Type != "any" || c.AccessRules[1].Action != "block" {
		t.Errorf("access rules %+v", c.AccessRules)
	}
	if r.ExpiresAt == nil || r.ExpiresAt.Unix() != c.Exp || r.RefreshAfter == nil || !r.RefreshAfter.Before(*r.ExpiresAt) {
		t.Errorf("expires %v refresh %v", r.ExpiresAt, r.RefreshAfter)
	}
	// the fake plays it (signature checked)
	fx.f.VerifySignatures = true
	resp, err := fx.f.Server.Client().Get(r.HLSURL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("playback %v %v", resp, err)
	}
	resp.Body.Close()
}

// TC4: past half its life the heartbeat returns a new token, not before.
func TestHeartbeatRefreshesToken(t *testing.T) {
	fx := newRemote(t)
	ctx := context.Background()
	r := fx.liveTune("u", fx.chans[0])
	fx.advance(20 * time.Minute)
	b, err := fx.c.Heartbeat(ctx, viewer("u"), r.SessionID)
	if err != nil || b.Token != "" || b.State != "live" {
		t.Fatalf("at 20 min: %+v %v", b, err)
	}
	fx.advance(11 * time.Minute)
	b, err = fx.c.Heartbeat(ctx, viewer("u"), r.SessionID)
	if err != nil || b.Token == "" || b.HLSURL == "" || b.ExpiresAt == nil || !b.ExpiresAt.After(*r.ExpiresAt) {
		t.Fatalf("at 31 min: %+v %v", b, err)
	}
	if _, err := fx.c.Heartbeat(ctx, viewer("someone-else"), r.SessionID); err == nil {
		t.Error("another user's heartbeat was accepted")
	}
}

// TC5: a new video (front door down past timeoutSeconds) → new token and URL.
func TestHeartbeatAfterVideoChange(t *testing.T) {
	fx := newRemote(t)
	ctx := context.Background()
	r := fx.liveTune("u", fx.chans[0])
	p := fx.last(fx.chans[0])
	uid := fx.cfInput(p.ID)
	fx.f.EndLive(uid)
	nv := fx.f.GoLive(uid)
	fx.advance(tvremote.LivePoll + time.Second)
	fx.reconcile()
	b, err := fx.c.Heartbeat(ctx, viewer("u"), r.SessionID)
	if err != nil || b.Token == "" {
		t.Fatalf("%+v %v", b, err)
	}
	if c, _ := cfClaims(b.Token); c.Sub != nv {
		t.Errorf("sub %s, want %s", c.Sub, nv)
	}
}

// TC6: no heartbeat for 150 s ends the session; the push then idles out.
func TestSessionExpiresWithoutHeartbeat(t *testing.T) {
	fx := newRemote(t)
	r := fx.liveTune("u", fx.chans[0])
	fx.advance(tvremote.SessionTimeout + time.Second)
	fx.reconcile()
	_, err := fx.c.Heartbeat(context.Background(), viewer("u"), r.SessionID)
	if te, ok := err.(*tvremote.TuneError); !ok || te.Status != http.StatusGone {
		t.Fatalf("%v", err)
	}
	var reason string
	_ = fx.db.QueryRow(`SELECT end_reason FROM tv_remote_sessions WHERE id = $1`, r.SessionID).Scan(&reason)
	if reason != "timeout" {
		t.Errorf("reason %q", reason)
	}
}

// TC7: rotating the key deletes the old one (old tokens stop playing);
// sessions get a token from the new key at their next heartbeat.
func TestRotateSigningKey(t *testing.T) {
	fx := newRemote(t)
	ctx := context.Background()
	fx.f.VerifySignatures = true
	r := fx.liveTune("u", fx.chans[0])
	res, err := fx.c.RotateSigningKey(ctx, "admin")
	if err != nil || !res.OldDeleted || res.OldKeyID != fx.kid || res.KeyID == fx.kid {
		t.Fatalf("%+v %v", res, err)
	}
	if _, ok := fx.f.Keys[fx.kid]; ok {
		t.Fatal("the old key still exists at Cloudflare")
	}
	resp, _ := fx.f.Server.Client().Get(r.HLSURL)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an old token still plays: %d", resp.StatusCode)
	}
	resp.Body.Close()
	b, err := fx.c.Heartbeat(ctx, viewer("u"), r.SessionID)
	if err != nil || b.Token == "" {
		t.Fatalf("no new token: %+v %v", b, err)
	}
	if c, _ := cfClaims(b.Token); c.Kid != res.KeyID {
		t.Errorf("kid %s", c.Kid)
	}
	resp, _ = fx.f.Server.Client().Get(b.HLSURL)
	if resp.StatusCode != 200 {
		t.Errorf("the new token does not play: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TC8: while the push starts the tune answers 202; retrying is the same session.
func TestTuneIdempotent202(t *testing.T) {
	fx := newRemote(t)
	a, te := fx.tune("u", fx.chans[0], "phone")
	if te != nil || a.State != "starting" || a.RetryAfter != 3 || a.SessionID == "" {
		t.Fatalf("%+v %v", a, te)
	}
	b, _ := fx.tune("u", fx.chans[0], "phone")
	if b.SessionID != a.SessionID {
		t.Fatalf("a retry opened a new session")
	}
	fx.goLive(fx.chans[0])
	c, _ := fx.tune("u", fx.chans[0], "phone")
	if c.SessionID != a.SessionID || c.State != "live" {
		t.Fatalf("%+v", c)
	}
	var n int
	_ = fx.db.QueryRow(`SELECT count(*) FROM tv_remote_sessions`).Scan(&n)
	if n != 1 {
		t.Errorf("sessions %d", n)
	}
}

// EC1: losing access mid-session: the next heartbeat answers 403 and ends it.
func TestHeartbeatAfterAccessLost(t *testing.T) {
	fx := newRemote(t)
	r := fx.liveTune("u", fx.chans[0])
	v := tvremote.Viewer{UserID: "u", CanRead: func(*string) bool { return false }}
	_, err := fx.c.Heartbeat(context.Background(), v, r.SessionID)
	if te, ok := err.(*tvremote.TuneError); !ok || te.Status != http.StatusForbidden {
		t.Fatalf("%v", err)
	}
	var ended *string
	_ = fx.db.QueryRow(`SELECT ended_at FROM tv_remote_sessions WHERE id = $1`, r.SessionID).Scan(&ended)
	if ended == nil {
		t.Error("session not ended")
	}
}

// Post-review: the viewers rule and the channel's enabled flag bind the whole
// session, not only the tune: a heartbeat after either changed mints no new
// token and ends the session.
func TestHeartbeatAfterGatesChange(t *testing.T) {
	cases := []struct {
		name   string
		change func(fx *remoteFx)
	}{
		{"viewers narrowed to admins", func(fx *remoteFx) { fx.rules(map[string]any{"viewers": "admins"}) }},
		{"viewers narrowed to a list", func(fx *remoteFx) {
			fx.rules(map[string]any{"viewers": "listed", "viewer_users": []string{"someone-else"}})
		}},
		{"channel switched off", func(fx *remoteFx) {
			fx.exec(`UPDATE channels SET enabled = false WHERE id = $1`, fx.chans[0])
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newRemote(t)
			r := fx.liveTune("u", fx.chans[0])
			tc.change(fx)
			fx.advance(31 * time.Minute) // a token would be due
			b, err := fx.c.Heartbeat(context.Background(), viewer("u"), r.SessionID)
			if te, ok := err.(*tvremote.TuneError); !ok || te.Status != http.StatusForbidden || te.Code != "remote_not_allowed" {
				t.Fatalf("heartbeat: %+v %v", b, err)
			}
			if b.Token != "" || b.HLSURL != "" {
				t.Fatalf("a token was minted: %+v", b)
			}
			var ended *string
			_ = fx.db.QueryRow(`SELECT ended_at FROM tv_remote_sessions WHERE id = $1`, r.SessionID).Scan(&ended)
			if ended == nil {
				t.Error("session not ended")
			}
		})
	}
}

// The listed rule matches a viewer's username on the heartbeat as on the tune.
func TestHeartbeatListedViewerByName(t *testing.T) {
	fx := newRemote(t)
	r := fx.liveTune("u", fx.chans[0])
	fx.rules(map[string]any{"viewers": "listed", "viewer_users": []string{"amina"}})
	v := viewer("u")
	v.Name = "Amina"
	fx.advance(31 * time.Minute)
	if b, err := fx.c.Heartbeat(context.Background(), v, r.SessionID); err != nil || b.Token == "" {
		t.Fatalf("listed viewer refused: %+v %v", b, err)
	}
}
