package discovery

// Epic 42 story 42.2: GET /api/discovery/home, the home's identity for the apps.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/compat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

func homeRouter(h *Handler, perMin int) http.Handler {
	r := chi.NewRouter()
	h.MountHome(r, perMin)
	return r
}

func getHome(r http.Handler, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/discovery/home", nil)
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// Anonymous: the exact shape, and nothing else (no user, no library, no address but the
// home's own).
func TestDiscoveryHomeShape(t *testing.T) {
	h := &Handler{Store: discovery.NewMemoryPairingStore(), Minter: &fakeMinter{},
		Home: func(context.Context, *http.Request) HomeInfo {
			return HomeInfo{ID: "0a1b2c3d-0000-4000-8000-000000000000", Name: "Kitchen", Version: "1.6.0",
				URLs: []string{"http://192.168.1.5:8088", "http://studio.local:8088/", "http://192.168.1.5:8088",
					"ftp://nope", "https://kitchen.relay.maktaba.tv"}}
		}}
	rec := getHome(homeRouter(h, -1), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 8 {
		t.Fatalf("keys = %v, want id, name, version, urls, sign_in, api_level, api_level_oldest, client_floor only", got)
	}
	if got["id"] != "0a1b2c3d-0000-4000-8000-000000000000" || got["name"] != "Kitchen" || got["version"] != "1.6.0" {
		t.Fatalf("identity = %v", got)
	}
	urls, _ := got["urls"].([]any)
	want := []string{"http://192.168.1.5:8088", "http://studio.local:8088", "https://kitchen.relay.maktaba.tv"}
	if len(urls) != len(want) {
		t.Fatalf("urls = %v, want %v", urls, want)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("urls = %v, want %v (LAN first, each once, then the relay)", urls, want)
		}
	}
	signIn, _ := got["sign_in"].(map[string]any)
	if len(signIn) != 2 || signIn["password"] != true || signIn["code"] != true {
		t.Fatalf("sign_in = %v", got["sign_in"])
	}
}

// Without a minter a code cannot sign anything in: sign_in.code is false.
func TestDiscoveryHomeCodeNeedsAMinter(t *testing.T) {
	rec := getHome(homeRouter(&Handler{}, -1), nil)
	var got homeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.SignIn.Password || got.SignIn.Code {
		t.Fatalf("sign_in = %+v", got.SignIn)
	}
}

// A home that knows no address of its own answers with the one it was reached at
// (https when the request came over TLS or through a TLS proxy).
func TestDiscoveryHomeFallsBackToTheRequestOrigin(t *testing.T) {
	r := homeRouter(&Handler{}, -1)
	cases := []struct {
		mod  func(*http.Request)
		want string
	}{
		{func(q *http.Request) { q.Host = "192.168.1.5:8088" }, "http://192.168.1.5:8088"},
		{func(q *http.Request) { q.Host = "home.example"; q.TLS = &tls.ConnectionState{} }, "https://home.example"},
		{func(q *http.Request) { q.Host = "home.example"; q.Header.Set("X-Forwarded-Proto", "https") }, "https://home.example"},
	}
	for _, c := range cases {
		var got homeResponse
		_ = json.Unmarshal(getHome(r, c.mod).Body.Bytes(), &got)
		if len(got.URLs) != 1 || got.URLs[0] != c.want {
			t.Errorf("urls = %v, want [%s]", got.URLs, c.want)
		}
	}
	var got map[string]any
	_ = json.Unmarshal(getHome(r, func(q *http.Request) { q.Host = "" }).Body.Bytes(), &got)
	if urls, ok := got["urls"].([]any); !ok || len(urls) != 0 {
		t.Errorf("no host: urls = %v, want []", got["urls"])
	}
}

// Rate-limited per IP, like the other public routes.
func TestDiscoveryHomeIsRateLimited(t *testing.T) {
	r := homeRouter(&Handler{}, 2)
	from := func(q *http.Request) { q.RemoteAddr = "192.0.2.9:4000" }
	codes := []int{}
	for i := 0; i < 4; i++ {
		codes = append(codes, getHome(r, from).Code)
	}
	if codes[0] != http.StatusOK || codes[3] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v", codes)
	}
	// another address is not held back
	if c := getHome(r, func(q *http.Request) { q.RemoteAddr = "192.0.2.10:4000" }).Code; c != http.StatusOK {
		t.Fatalf("another IP = %d", c)
	}
}

// The home's live TV port is in the answer when the home knows it (Epic 59 story 59.08: a
// port set in the container, or moved off a taken one), so its machines and apps use it.
func TestDiscoveryHomeStreamPort(t *testing.T) {
	h := &Handler{Store: discovery.NewMemoryPairingStore(),
		Home: func(context.Context, *http.Request) HomeInfo {
			return HomeInfo{ID: "0a1b2c3d-0000-4000-8000-000000000000", Name: "NAS", Version: "1.6.0",
				URLs: []string{"http://192.168.1.5:18088"}, StreamPort: 18081}
		}}
	rec := getHome(homeRouter(h, -1), nil)
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["stream_port"] != float64(18081) || len(got) != 9 {
		t.Fatalf("answer = %v, want stream_port 18081 beside the eight keys", got)
	}
}

// T01 (Epic 58 story 58.01): the compatibility contract, with no sign-in: the home's API
// level, the oldest it still answers, and its client floor (an empty object by default).
func TestDiscoveryHomeCompatibilityContract(t *testing.T) {
	var got map[string]any
	rec := getHome(homeRouter(&Handler{}, -1), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 without a sign-in", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["api_level"] != float64(compat.Level) || got["api_level_oldest"] != float64(compat.Oldest) {
		t.Fatalf("levels = %v / %v, want %d / %d", got["api_level"], got["api_level_oldest"], compat.Level, compat.Oldest)
	}
	if floor, ok := got["client_floor"].(map[string]any); !ok || len(floor) != len(compat.ReleaseFloor) {
		t.Fatalf("client_floor = %#v, want the release's floor as an object", got["client_floor"])
	}

	// a home release that raises a floor publishes it
	h := &Handler{ClientFloor: compat.Floor{"tv-web/webos": "1.4.0"}}
	got = nil
	_ = json.Unmarshal(getHome(homeRouter(h, -1), nil).Body.Bytes(), &got)
	floor, _ := got["client_floor"].(map[string]any)
	if len(floor) != 1 || floor["tv-web/webos"] != "1.4.0" {
		t.Fatalf("client_floor = %v", got["client_floor"])
	}
}
