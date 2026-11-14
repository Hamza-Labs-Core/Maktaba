package router_test

// Epic 58 story 58.17 T03: the tv-clients rehearsal. The home is driven the way each TV
// drives it, with the request shapes the platforms' own tests record (each TV's
// X-Maktaba-Client, and the device_caps of shared/playback/caps-vectors.json), behind the
// production router on a real migrated Postgres, directly (the LAN) and through the relay path
// (the relay agent on this machine forwards with X-Maktaba-Via: relay from loopback):
//
//  1. Compat (TestTVClientsCompat): discovery is read; each TV's build decides carry on, an
//     app a year older than the home carries on, a level outside the home's range shows Update
//     the home or Update this app, a build below client_floor is refused 426 at sign-in, and a
//     home rolled back to a release from before the contract shows Update the home while its
//     sign-in still works.
//  2. Cloud code (TestTVClientsCloudCode): a viewer approves, the TV redeems on the home over
//     the LAN and through the relay path, with each TV's header, and is the viewer, never an
//     admin. The cloud's half, through the tunnel, is the cloud rehearsal's tv-code (T04).
//  3. Sessions (TestTVClientsSessions): each platform's recorded device_caps reach the
//     streaming service as sent, a capped relay asks for less, and an app from before 58.02
//     (no device_caps) still opens; the modes 58.02's table names are the vectors' own
//     (TestTVClientsModesAreTheStorys, and streaming's TestCapsVectors in the same job).
//  4. Cast grants and 5. Parental are TestPGCastGrant* and TestP58Bypass* (58.07, 58.14),
//     which the job runs with these.
//
// The job is `tv-clients` of .github/workflows/_integration.yml; tools/ci/tv-clients-tests.txt
// lists every test it must see pass.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/compat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

// tvRehearsalApps are the TV builds the rehearsal plays, as each sends X-Maktaba-Client.
var tvRehearsalApps = []struct{ name, header string }{
	{"fire tv", "tv/1.4.0 (firetv; tv)"},
	{"samsung /tv/", "tv-web/1.4.0 (tizen; tv)"},
	{"lg /tv/", "tv-web/1.4.0 (webos; tv)"},
	{"roku", "roku/1.4.0 (roku; tv)"},
}

// tvPath is how a request reaches the home.
type tvPath struct {
	name string
	// via sets the request up as it arrives that way
	via func(r *http.Request)
}

var tvPaths = []tvPath{
	{"lan", func(r *http.Request) { r.RemoteAddr = "192.168.1.40:51000" }},
	{"relay", func(r *http.Request) {
		r.RemoteAddr = "127.0.0.1:49152"
		r.Header.Set("X-Maktaba-Via", "relay")
	}},
}

// tvApp is what an app knows about itself, from its header, built for level built (min: the
// oldest home level it can use).
func tvApp(header string, minLevel, built int) compat.App {
	info := client.Parse(header)
	return compat.App{Name: info.App, Platform: info.Platform, Version: info.Version, MinAPILevel: minLevel, BuiltForAPILevel: built}
}

func TestTVClientsCompat(t *testing.T) {
	t.Parallel()
	db, _ := clustertest.ScratchDB(t)
	set := keys.NewSet(time.Hour)
	k, err := keys.Generate(keys.MinBits)
	if err != nil {
		t.Fatal(err)
	}
	set.Replace(k)
	// a security fix raised the floor of one platform: the Roku channel 1.4.0 is refused
	floor := compat.Floor{"roku/roku": "1.4.1"}
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore(), ClientFloor: floor})
	ah := router.MountP9(r, router.P9Deps{DB: db, Keys: set})
	light := argon2id.DefaultParams()
	light.Memory, light.Time = 64, 1
	ah.Users.Params, ah.RefreshTokens.Params = light, light
	router.MountP10(r, router.P10Deps{DB: db, Keys: set, ClientFloor: floor, Logger: slog.Default()})
	us := users.New(db)
	us.Params = light
	if _, err := us.Create(t.Context(), users.CreateInput{Username: "tv-viewer", Password: "correct horse battery"}); err != nil {
		t.Fatal(err)
	}

	do := func(p tvPath, header, method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(client.Header, header)
		p.via(req)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	login := map[string]string{"username": "tv-viewer", "password": "correct horse battery"}

	for _, p := range tvPaths {
		for _, app := range tvRehearsalApps {
			name := app.name + " (" + p.name + ")"
			code, raw := do(p, app.header, http.MethodGet, "/api/discovery/home", nil)
			if code != http.StatusOK {
				t.Fatalf("%s: discovery = %d %v", name, code, raw)
			}
			b, _ := json.Marshal(raw)
			var home compat.Home
			if err := json.Unmarshal(b, &home); err != nil || home.APILevel != compat.Level || home.APILevelOldest != compat.Oldest {
				t.Fatalf("%s: discovery's contract %+v (%v)", name, home, err)
			}
			refused := strings.HasPrefix(app.header, "roku/")

			// this build at this home; an app a year older (built for the oldest level the
			// home still answers) at this home
			want := compat.Carry
			if refused {
				want = compat.UpdateApp
			}
			for _, built := range []int{compat.Level, compat.Oldest} {
				if got := compat.Decide(home, tvApp(app.header, compat.Oldest, built)); got != want {
					t.Errorf("%s built for level %d: %s, want %s", name, built, got, want)
				}
			}
			// a level outside the range: an app that needs a newer home, an app built for a
			// level this home no longer answers
			if got := compat.Decide(home, tvApp(app.header, compat.Level+1, compat.Level+1)); got != compat.UpdateHome {
				t.Errorf("%s needing level %d: %s, want update_home", name, compat.Level+1, got)
			}
			if got := compat.Decide(home, tvApp(app.header, compat.Oldest-1, compat.Oldest-1)); got != compat.UpdateApp {
				t.Errorf("%s built for level %d: %s, want update_app", name, compat.Oldest-1, got)
			}

			// sign-in: the floor refuses the Roku build with 426, naming the floor; the others sign in
			code, out := do(p, app.header, http.MethodPost, "/api/auth/login", login)
			switch {
			case refused && (code != http.StatusUpgradeRequired || out["code"] != compat.Code || out["floor"] != "1.4.1"):
				t.Errorf("%s: sign-in below the floor = %d %v, want 426 client_too_old", name, code, out)
			case !refused && (code != http.StatusOK || out["refresh_token"] == nil):
				t.Errorf("%s: sign-in = %d %v, want 200 with tokens", name, code, out)
			}
			// the updated build signs in
			if refused {
				newer := strings.Replace(app.header, "1.4.0", "1.4.1", 1)
				if code, out := do(p, newer, http.MethodPost, "/api/auth/login", login); code != http.StatusOK {
					t.Errorf("%s: the updated build's sign-in = %d %v", name, code, out)
				}
				if got := compat.Decide(home, tvApp(newer, compat.Oldest, compat.Level)); got != compat.Carry {
					t.Errorf("%s: the updated build decides %s", name, got)
				}
			}

			// rollback: the home went back to a release from before the contract, whose
			// discovery answer has none of its fields (as recorded from v0.3.120). The app shows
			// Update the home, and its sign-in on that home is still the home's to answer.
			var old compat.Home
			if err := json.Unmarshal([]byte(`{"id":"h","name":"Home","version":"0.3.120","urls":["http://192.168.1.10:8080"],"sign_in":{"password":true,"code":true}}`), &old); err != nil {
				t.Fatal(err)
			}
			if got := compat.Decide(old, tvApp(app.header, compat.Oldest, compat.Level)); got != compat.UpdateHome {
				t.Errorf("%s at a rolled-back home: %s, want update_home", name, got)
			}
		}
	}
}

// tvCapsVectors reads shared/playback/caps-vectors.json: each vector's id, profile, recorded
// device_caps and the verdicts the home decides for them.
func tvCapsVectors(t *testing.T) []struct {
	ID         string                     `json:"id"`
	Profile    string                     `json:"profile"`
	DeviceCaps json.RawMessage            `json:"device_caps"`
	Verdicts   map[string]json.RawMessage `json:"verdicts"`
} {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "playback", "caps-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Vectors []struct {
			ID         string                     `json:"id"`
			Profile    string                     `json:"profile"`
			DeviceCaps json.RawMessage            `json:"device_caps"`
			Verdicts   map[string]json.RawMessage `json:"verdicts"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Vectors) < 10 {
		t.Fatalf("only %d vectors", len(doc.Vectors))
	}
	return doc.Vectors
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// The modes 58.02's table names, for the sources the rehearsal names, are the vectors' own:
// Fire TV plays HEVC HDR10 direct, Samsung gets Dolby Vision profile 8 as an HDR10 remux, Cast
// gets HEVC as HLS fMP4, Roku gets AV1 as DASH. Streaming's TestCapsVectors decides every
// vector the same way in this job.
func TestTVClientsModesAreTheStorys(t *testing.T) {
	want := map[string]map[string]string{
		"firetv-stick-4k-max-2":   {"hevc-2160-hdr10-eac3-mkv": `{"mode":"direct"}`},
		"tizen-2022-qn90b":        {"hevc-2160-dv81-truehd-mkv": `{"mode":"remux","segments":"fmp4","audio":"eac3","strip_dv":true}`},
		"cast-google-tv-streamer": {"hevc-2160-hdr10-eac3-mkv": `{"mode":"remux","segments":"fmp4"}`},
		"roku-ultra-2024":         {"av1-2160-hdr10-opus-mkv": `{"mode":"remux","format":"dash","audio":"aac"}`},
	}
	seen := 0
	for _, v := range tvCapsVectors(t) {
		for src, verdict := range want[v.ID] {
			seen++
			if got := v.Verdicts[src]; !sameJSON(got, []byte(verdict)) {
				t.Errorf("%s, %s: the vectors say %s, the story %s", v.ID, src, got, verdict)
			}
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of the story's %d cases in the vectors", seen, len(want))
	}
}

func TestTVClientsSessions(t *testing.T) {
	t.Parallel()
	h := setupHome(t)
	with := tvGrantFront(t, h)
	var via tvPath
	var relayCap string
	h.as = func(req *http.Request, p *principal.Principal) *principal.Principal {
		via.via(req)
		if relayCap != "" {
			req.Header.Set("X-Maktaba-Relay-Cap-Kbps", relayCap)
		}
		return p
	}
	last := func() (n int, caps json.RawMessage, profile string, maxKbps int) {
		h.streaming.mu.Lock()
		defer h.streaming.mu.Unlock()
		n = len(h.streaming.reqs)
		if n == 0 {
			return 0, nil, "", 0
		}
		r := h.streaming.reqs[n-1]
		return n, r.DeviceCaps, r.ClientProfile, r.MaxBitrateKbps
	}
	open := func(name, who, body string) {
		t.Helper()
		before, _, _, _ := last()
		var rec *httptest.ResponseRecorder
		if who == "cast" {
			// the receiver opens its session with the grant its sender minted
			g := tvMint(t, h, "viewer", `{"video_id":"`+pfKid+`"}`)
			rec = with(g, http.MethodPost, "/api/stream/sessions", body)
		} else {
			rec = h.raw(who, http.MethodPost, "/api/stream/sessions", body)
		}
		if rec.Code != http.StatusCreated {
			t.Fatalf("%s: open = %d %s", name, rec.Code, rec.Body.String())
		}
		if n, _, _, _ := last(); n != before+1 {
			t.Fatalf("%s: the streaming service was asked %d times, want once", name, n-before)
		}
	}

	vectors := tvCapsVectors(t)
	for _, p := range tvPaths {
		via = p
		for _, v := range vectors {
			name := v.ID + " (" + p.name + ")"
			who := "viewer"
			if v.Profile == "cast" {
				who = "cast"
			}
			body := `{"video_id":"` + pfKid + `","client_profile":"` + v.Profile + `","device_caps":` + string(v.DeviceCaps) + `}`
			open(name, who, body)
			_, caps, profile, _ := last()
			if profile != v.Profile || !sameJSON(caps, v.DeviceCaps) {
				t.Errorf("%s: the streaming service got profile %q and %s, want %q and the recorded report %s", name, profile, caps, v.Profile, v.DeviceCaps)
			}
		}
		// an app from before 58.02 sends its profile alone: the session opens on it
		open("an app without device_caps ("+p.name+")", "viewer", `{"video_id":"`+pfKid+`","client_profile":"firetv"}`)
		if _, caps, profile, _ := last(); caps != nil || profile != "firetv" {
			t.Errorf("%s: the streaming service got %q and %s, want firetv and no report", p.name, profile, caps)
		}
	}

	// a relay that caps the plan's bitrate: the session asks for what fits, with the report
	via, relayCap = tvPaths[1], "8000"
	v := vectors[0]
	open("capped relay", "viewer", `{"video_id":"`+pfKid+`","client_profile":"`+v.Profile+`","device_caps":`+string(v.DeviceCaps)+`}`)
	if _, caps, _, kbps := last(); kbps != 8000*100/110 || !sameJSON(caps, v.DeviceCaps) {
		t.Errorf("capped relay: max %d kbps and %s, want %d and the report", kbps, caps, 8000*100/110)
	}
	// the same header on the LAN is no relay's: nothing is capped
	via = tvPaths[0]
	open("cap header on the lan", "viewer", `{"video_id":"`+pfKid+`","client_profile":"`+v.Profile+`"}`)
	if _, _, _, kbps := last(); kbps != 0 {
		t.Errorf("a cap header on the LAN capped the session at %d kbps", kbps)
	}
}

func TestTVClientsCloudCode(t *testing.T) {
	h, agent, _ := setupCloudCode(t)
	codes := []string{"FTVA2345", "SMSA2345", "GTVA2345", "RKYA2345", "FTVB2345", "SMSB2345", "GTVB2345", "RKYB2345"} // the cloud's alphabet: no 0, 1, I, L, O or U
	i := 0
	for _, p := range tvPaths {
		for _, app := range tvRehearsalApps {
			code := codes[i]
			i++
			name := app.name + " (" + p.name + ")"
			// the phone at home approves as the viewer
			if rec := ccDo(h, "viewer", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"`+code+`"}`); rec.Code != http.StatusOK {
				t.Fatalf("%s: approve = %d %s", name, rec.Code, rec.Body.String())
			}
			grant := agent.grant(code)
			if grant == "" {
				t.Fatalf("%s: the cloud got no grant", name)
			}
			// the TV redeems the grant on the home, as it reaches it
			body := `{"device_code":"` + grant + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/pairing/device/token", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(client.Header, app.header)
			p.via(req)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: redeem = %d %s", name, rec.Code, rec.Body.String())
			}
			var tok struct {
				AccessToken string `json:"access_token"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &tok)
			if tok.AccessToken == "" {
				t.Fatalf("%s: no access token: %s", name, rec.Body.String())
			}
			c := ccTokenClaims(t, tok.AccessToken)
			if c.Usr != ccViewer || c.IsAdmin || !c.Rst || strings.Join(c.Lib, ",") != ccLibA {
				t.Errorf("%s: the TV signed in as %+v, want the viewer with library A, never an admin", name, c)
			}
			// the grant is single-use, whichever way it comes back
			again := httptest.NewRequest(http.MethodPost, "/api/pairing/device/token", strings.NewReader(body))
			again.Header.Set("Content-Type", "application/json")
			tvPaths[1-indexOf(p.name)].via(again)
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, again)
			if rec.Code == http.StatusOK {
				t.Errorf("%s: the grant redeemed twice", name)
			}
		}
	}
}

func indexOf(path string) int {
	for i, p := range tvPaths {
		if p.name == path {
			return i
		}
	}
	return 0
}
