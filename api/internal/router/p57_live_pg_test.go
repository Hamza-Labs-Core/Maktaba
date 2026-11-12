package router_test

// Epic 57 story 57.10: live TV behind the production router, against a real migrated
// Postgres (MAKTABA_TEST_PG_URL). Each route as no one, a viewer, a restricted child,
// that child's guardian, another guardian and an admin; what the child's lists and
// guide hold; what a programme above the limit looks like.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/signedurl"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const liveSecret = "SECRET-SIXTEEN"

type liveEnv struct {
	t     *testing.T
	db    *sql.DB
	h     http.Handler
	users map[string]string
	token string
	// ch9 plays a 9 programme now and a 16 one next; ch16 and chNone are above the
	// child's limit; chNow plays a 16 programme now and a 9 one next.
	ch9, ch16, chNone, chNow string
	boundary                 time.Time
}

var liveRoles = []string{"", "viewer", "child", "guardian", "other", "admin"}

func setupLive(t *testing.T) *liveEnv {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	e := &liveEnv{t: t, db: db, users: map[string]string{}}
	for _, who := range liveRoles[1:] {
		e.users[who] = uuid.NewString()
		e.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, $2, 'x', $3)`, e.users[who], "live-"+who, who == "admin")
	}
	e.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1)`, e.users["child"])
	e.exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1, $2)`, e.users["child"], e.users["guardian"])

	e.ch9, e.ch16, e.chNone, e.chNow = uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	e.exec(`INSERT INTO channels (id, number, name, slug, mode, age_tier) VALUES
		($1, 901, 'Nine', 'nine', 'shuffle', 9), ($2, 916, 'Sixteen', 'sixteen', 'shuffle', 16),
		($3, 900, 'Unrated', 'unrated', 'shuffle', NULL), ($4, 902, 'NineNow', 'nine-now', 'shuffle', 9)`,
		e.ch9, e.ch16, e.chNone, e.chNow)
	now := time.Now().UTC().Truncate(time.Second)
	e.boundary = now.Add(30 * time.Minute)
	prog := func(ch string, seq int, from, to time.Time, title string, tier any) {
		e.exec(`INSERT INTO channel_programs (channel_id, seq, kind, start_at, end_at, source_duration, title_snapshot, age_tier, sched_gen)
			VALUES ($1, $2, 'slate', $3, $4, 3600000, jsonb_build_object('title', $5::text), $6, tv_live_gen())`,
			ch, seq, from, to, title, tier)
	}
	prog(e.ch9, 0, now.Add(-30*time.Minute), e.boundary, "Open nine", 9)
	prog(e.ch9, 1, e.boundary, now.Add(90*time.Minute), liveSecret, 16)
	prog(e.ch9, 2, now.Add(90*time.Minute), now.Add(150*time.Minute), "Open again", nil) // the channel's tier
	prog(e.ch16, 0, now.Add(-30*time.Minute), now.Add(time.Hour), liveSecret+" on sixteen", nil)
	prog(e.chNone, 0, now.Add(-30*time.Minute), now.Add(time.Hour), liveSecret+" unrated", nil)
	prog(e.chNow, 0, now.Add(-30*time.Minute), e.boundary, liveSecret+" now", 16)
	prog(e.chNow, 1, e.boundary, now.Add(90*time.Minute), "Open later", 9)

	key, _, err := signedurl.LoadOrCreateKey(filepath.Join(t.TempDir(), "stream.key"))
	if err != nil {
		t.Fatal(err)
	}
	e.token = "local-" + uuid.NewString()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(e.token), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", tokenFile)

	// the real gate over the real registry (story 57.03): the live TV routes answer a
	// restricted account only because the registry names them
	gate := parental.NewGate()
	gate.SetCache(parental.NewCache(parental.NewStore(db)))
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore(), Parental: gate})
	router.MountP27(r, router.P27Deps{DB: db, DataDir: t.TempDir(), ChannelSigner: signedurl.FromKey(key)})
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if who := req.Header.Get("X-Test-As"); who != "" {
			req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
				UserID: e.users[who], IsAdmin: who == "admin" || who == "admintoken", AccessAllLibraries: true, Source: principal.SourceCookie}))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

func (e *liveEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *liveEnv) do(who, method, path, body string, hdr ...string) (int, string, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	if who != "" {
		req.Header.Set("X-Test-As", who)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, rec.Body.String(), out
}

// codes asks one route as every role and checks each status. want is in liveRoles'
// order: no one, viewer, child, guardian, other guardian, admin.
func (e *liveEnv) codes(method, path, body string, want [6]int) {
	e.t.Helper()
	for i, who := range liveRoles {
		code, raw, _ := e.do(who, method, path, body)
		if code != want[i] {
			name := who
			if name == "" {
				name = "no one"
			}
			e.t.Errorf("%s %s as %s = %d, want %d: %s", method, path, name, code, want[i], raw)
		}
		if who == "child" && strings.Contains(raw, liveSecret) {
			e.t.Errorf("%s %s shows the child a programme above its limit: %s", method, path, raw)
		}
	}
}

// T02, T06 and the role test of every route the story guards or adds.
func TestP57LiveTVPG(t *testing.T) {
	e := setupLive(t)
	ch := "/api/channels/"
	open := func(noOne int) [6]int { return [6]int{noOne, 200, 200, 200, 200, 200} }
	hidden := func(noOne int) [6]int { return [6]int{noOne, 200, 404, 200, 200, 200} }
	noOne, _, _ := e.do("", "GET", ch+e.ch9, "")
	if noOne != 401 && noOne != 403 {
		t.Fatalf("GET a channel as no one = %d, want a refusal", noOne)
	}

	t.Run("who may read each channel route", func(t *testing.T) {
		e.codes("GET", "/api/channels", "", open(noOne))
		e.codes("GET", "/api/channels/guide", "", open(noOne))
		e.codes("GET", "/api/channels/now", "", open(noOne))
		for _, tail := range []string{"", "/schedule", "/guide", "/access"} {
			e.codes("GET", ch+e.ch9+tail, "", open(noOne))
			e.codes("GET", ch+e.chNow+tail, "", open(noOne))
			e.codes("GET", ch+e.ch16+tail, "", hidden(noOne))
			e.codes("GET", ch+e.chNone+tail, "", hidden(noOne))
		}
		// a channel that does not exist answers the child exactly as a hidden one does
		_, hiddenBody, _ := e.do("child", "GET", ch+e.ch16, "")
		ghost := uuid.NewString()
		_, ghostBody, _ := e.do("child", "GET", ch+ghost, "")
		same := func(raw, id string) string {
			var m map[string]any
			_ = json.Unmarshal([]byte(raw), &m)
			delete(m, "requestId")
			b, _ := json.Marshal(m)
			return strings.ReplaceAll(string(b), id, "X")
		}
		if same(hiddenBody, e.ch16) != same(ghostBody, ghost) {
			t.Errorf("a hidden channel and an unknown one differ:\n%s\n%s", hiddenBody, ghostBody)
		}
	})

	t.Run("T02 the child lists only the channel within its limit", func(t *testing.T) {
		for _, path := range []string{"/api/channels", "/api/channels/guide", "/api/channels/now"} {
			_, raw, _ := e.do("child", "GET", path, "")
			if !strings.Contains(raw, e.ch9) || !strings.Contains(raw, e.chNow) {
				t.Errorf("%s: the child's own channels are missing: %s", path, raw)
			}
			if strings.Contains(raw, e.ch16) || strings.Contains(raw, e.chNone) {
				t.Errorf("%s lists a channel above the child's limit: %s", path, raw)
			}
			_, raw, _ = e.do("viewer", "GET", path, "")
			for _, id := range []string{e.ch9, e.ch16, e.chNone, e.chNow} {
				if !strings.Contains(raw, id) {
					t.Errorf("%s as a viewer misses channel %s", path, id)
				}
			}
		}
	})

	t.Run("T02 a programme above the limit is masked, its time kept", func(t *testing.T) {
		at := e.boundary.Format(time.RFC3339)
		for _, path := range []string{"/api/channels/guide?collapse=off", ch + e.ch9 + "/guide?collapse=off", ch + e.ch9 + "/schedule"} {
			_, raw, _ := e.do("child", "GET", path, "")
			if strings.Contains(raw, liveSecret) {
				t.Errorf("%s shows the title: %s", path, raw)
			}
			if !strings.Contains(raw, `"restricted":true`) || !strings.Contains(raw, at[:16]) {
				t.Errorf("%s: no masked programme at %s: %s", path, at, raw)
			}
			if !strings.Contains(raw, "Open nine") && !strings.Contains(path, "now") {
				t.Errorf("%s: the allowed programme lost its title: %s", path, raw)
			}
			_, raw, _ = e.do("guardian", "GET", path, "")
			if !strings.Contains(raw, liveSecret) || strings.Contains(raw, `"restricted":true`) {
				t.Errorf("%s as the guardian is masked: %s", path, raw)
			}
		}
		// what plays now on chNow is above the limit: no now_playing title anywhere
		for _, path := range []string{"/api/channels", ch + e.chNow, "/api/channels/now"} {
			if _, raw, _ := e.do("child", "GET", path, ""); strings.Contains(raw, liveSecret) {
				t.Errorf("%s shows what plays now above the limit: %s", path, raw)
			}
		}
	})

	t.Run("T06 the exports refuse a restricted account", func(t *testing.T) {
		for _, path := range []string{"/api/channels/xmltv", "/api/channels/playlist.m3u"} {
			e.codes("GET", path, "", [6]int{noOne, 200, 403, 200, 200, 200})
			_, _, out := e.do("child", "GET", path, "")
			if out["code"] != "restricted" {
				t.Errorf("%s as the child = %v, want code restricted", path, out)
			}
		}
	})

	t.Run("access and play", func(t *testing.T) {
		_, _, out := e.do("child", "GET", ch+e.ch9+"/access", "")
		until, _ := out["until"].(string)
		if out["restricted"] != true || out["allowed"] != true || until != e.boundary.Format(time.RFC3339) {
			t.Errorf("access to the 9 channel = %v, want allowed until %s", out, e.boundary.Format(time.RFC3339))
		}
		_, _, out = e.do("child", "GET", ch+e.chNow+"/access", "")
		until, _ = out["until"].(string)
		if out["allowed"] != false || until != e.boundary.Format(time.RFC3339) {
			t.Errorf("access while a 16 programme plays = %v, want refused until the boundary", out)
		}
		_, _, out = e.do("viewer", "GET", ch+e.chNow+"/access", "")
		if out["restricted"] != false || out["allowed"] != true || out["until"] != nil {
			t.Errorf("a viewer's access = %v", out)
		}

		e.codes("POST", ch+e.ch9+"/play", "", open(noOne))
		e.codes("POST", ch+e.ch16+"/play", "", hidden(noOne))
		e.codes("POST", ch+e.chNone+"/play", "", hidden(noOne))
		e.codes("POST", ch+e.chNow+"/play", "", [6]int{noOne, 200, 403, 200, 200, 200})
		_, _, out = e.do("child", "POST", ch+e.chNow+"/play", "")
		if out["code"] != "restricted" || out["reason"] != "programme" || out["retry_at"] != e.boundary.Format(time.RFC3339) {
			t.Errorf("play while a 16 programme plays = %v", out)
		}
		_, _, out = e.do("child", "POST", ch+e.ch9+"/play", "")
		u, _ := out["url"].(string)
		if !strings.HasPrefix(u, "/stream/channel/"+e.ch9+"/index.m3u8?sig=") || out["until"] != e.boundary.Format(time.RFC3339) {
			t.Errorf("play = %v", out)
		}
	})

	t.Run("who a channel URL is signed for", func(t *testing.T) {
		claims := func(who string) (int, map[string]any) {
			code, _, out := e.do(who, "POST", "/api/channels/"+e.ch9+"/play", "")
			u, _ := out["url"].(string)
			_, sig, _ := strings.Cut(u, "sig=")
			parts := strings.Split(sig, ".")
			if code != 200 || len(parts) != 3 {
				return code, nil
			}
			raw, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Fatal(err)
			}
			var c map[string]any
			_ = json.Unmarshal(raw, &c)
			return code, c
		}
		// an account: usr is the account, and no admin claim even for an admin (the
		// streaming service asks the API about every account)
		for _, who := range []string{"child", "admin"} {
			_, c := claims(who)
			if c["aud"] != "streaming-channel" || c["sub"] != e.ch9 || c["usr"] != e.users[who] || c["is_admin"] != nil {
				t.Errorf("%s's channel URL claims = %v", who, c)
			}
		}
		// the admin token (an admin with no account) says so in the signed claim
		if _, c := claims("admintoken"); c["usr"] != "admin" || c["is_admin"] != true || c["sub"] != e.ch9 {
			t.Errorf("the admin token's channel URL claims = %v", c)
		}
		// a sign-in with no account that is no admin gets no URL
		if code, c := claims("noaccount"); code != 403 || c != nil {
			t.Errorf("no account and no admin = %d %v, want 403", code, c)
		}
	})

	t.Run("the streaming service's question takes the local token only", func(t *testing.T) {
		q := parentalh.VerdictPath + "?channel=" + e.chNow + "&user=" + e.users["child"]
		e.codes("GET", q, "", [6]int{403, 403, 403, 403, 403, 403})
		if code, _, _ := e.do("admin", "GET", q, "", mw.LocalTokenHeader, "wrong"); code != 403 {
			t.Errorf("a wrong token = %d, want 403", code)
		}
		code, _, out := e.do("", "GET", q, "", mw.LocalTokenHeader, e.token)
		if code != 200 || out["restricted"] != true || out["channel_allowed"] != true || out["allowed"] != false ||
			out["until"] != e.boundary.Format(time.RFC3339) {
			t.Errorf("verdict for the child on a 16 programme = %d %v", code, out)
		}
		_, _, out = e.do("", "GET", parentalh.VerdictPath+"?channel="+e.ch16+"&user="+e.users["child"], "", mw.LocalTokenHeader, e.token)
		if out["channel_allowed"] != false || out["allowed"] != false {
			t.Errorf("verdict for the child on the 16 channel = %v", out)
		}
		_, _, out = e.do("", "GET", parentalh.VerdictPath+"?channel="+e.ch16+"&user="+e.users["viewer"], "", mw.LocalTokenHeader, e.token)
		if out["restricted"] != false || out["allowed"] != true {
			t.Errorf("verdict for a viewer = %v", out)
		}
		if code, _, _ := e.do("", "GET", parentalh.VerdictPath+"?channel=x&user=y", "", mw.LocalTokenHeader, e.token); code != 400 {
			t.Errorf("a malformed question = %d, want 400", code)
		}
	})

	t.Run("the tuner's allowed addresses are the admin's", func(t *testing.T) {
		// a restricted account gets the answer of a route that is not there (the
		// registry closes the TV's admin routes to it)
		admin := [6]int{noOne, 403, 404, 403, 403, 200}
		e.codes("GET", "/api/tv/hdhr/allowed", "", admin)
		e.codes("PUT", "/api/tv/hdhr/allowed", `{"addresses":["192.168.1.20"," 10.0.0.0/24 ","192.168.1.20"]}`, admin)
		_, raw, _ := e.do("admin", "GET", "/api/tv/hdhr/allowed", "")
		if !strings.Contains(raw, `["192.168.1.20","10.0.0.0/24"]`) {
			t.Errorf("stored addresses = %s", raw)
		}
		for _, bad := range []string{`{"addresses":["0.0.0.0/0"]}`, `{"addresses":["plex.local"]}`} {
			if code, _, _ := e.do("admin", "PUT", "/api/tv/hdhr/allowed", bad); code != 422 {
				t.Errorf("PUT %s = %d, want 422", bad, code)
			}
		}
	})

	t.Run("a rule and a changed limit hold on the next request", func(t *testing.T) {
		e.exec(`INSERT INTO parental_rules (id, user_id, effect, subject_kind, subject_id) VALUES ($1, $2, 'allow', 'channel', $3)`,
			uuid.NewString(), e.users["child"], e.ch16)
		e.exec(`UPDATE parental_policies SET version = version + 1 WHERE user_id = $1`, e.users["child"])
		if code, _, _ := e.do("child", "GET", ch+e.ch16, ""); code != 200 {
			t.Errorf("an allowed channel = %d, want 200", code)
		}
		e.exec(`INSERT INTO parental_rules (id, user_id, effect, subject_kind, subject_id) VALUES ($1, $2, 'block', 'channel', $3)`,
			uuid.NewString(), e.users["child"], e.ch9)
		e.exec(`UPDATE parental_policies SET version = version + 1 WHERE user_id = $1`, e.users["child"])
		if code, _, _ := e.do("child", "GET", ch+e.ch9, ""); code != 404 {
			t.Errorf("a blocked channel = %d, want 404", code)
		}
	})
}

// The remote viewing routes' check (POST /api/tv/remote/tune, …/heartbeat), which the
// handler asks before it tunes: by channel, and by the caller's own session.
func TestP57LiveRemoteGatePG(t *testing.T) {
	e := setupLive(t)
	gate := parentalh.RemoteGate(e.db, parental.NewLiveTV(e.db, nil))
	ask := func(who, channel, session string) int {
		req := httptest.NewRequest("POST", "/api/tv/remote/tune", nil).WithContext(context.Background())
		var p *principal.Principal
		if who != "" {
			p = &principal.Principal{UserID: e.users[who], IsAdmin: who == "admin" || who == "admintoken", AccessAllLibraries: true}
		}
		if err := gate(req, p, channel, session); err != nil {
			return err.Status
		}
		return 0
	}
	for _, who := range liveRoles {
		for _, c := range []struct {
			channel string
			child   int
		}{{e.ch9, 0}, {e.ch16, 404}, {e.chNone, 404}, {e.chNow, 403}} {
			want := 0
			if who == "child" {
				want = c.child
			}
			if got := ask(who, c.channel, ""); got != want {
				t.Errorf("tune %s as %q = %d, want %d", c.channel, who, got, want)
			}
		}
	}
	var cols int
	if err := e.db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'tv_remote_sessions'`).Scan(&cols); err != nil || cols == 0 {
		t.Skip("no tv_remote_sessions table")
	}
	session := uuid.NewString()
	if _, err := e.db.Exec(`INSERT INTO tv_remote_sessions (id, user_id, channel_id, device) VALUES ($1, $2, $3, 'test')`,
		session, e.users["child"], e.chNow); err != nil {
		t.Fatalf("insert a remote session: %v", err)
	}
	if got := ask("child", "", session); got != 403 {
		t.Errorf("heartbeat of a session on a 16 programme = %d, want 403", got)
	}
	if got := ask("child", "", uuid.NewString()); got != 0 {
		t.Errorf("heartbeat of an unknown session = %d, want the route's own answer", got)
	}
}
