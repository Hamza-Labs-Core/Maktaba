package relay

// Issue #827: dizqueTV behind the service door, its programmes built on the home. The token
// crosses the plugin boundary in neither direction: the plugin sends Plex ids and token-free
// records, the door writes the programme with the token, and every read the plugin makes is a
// projection with no secret in it (not a scrubbed copy of a stored body).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// dzSecret is dizqueTV's own copy of a Plex token, in its plex-servers (another one than the
// home's: a user may have registered the server with another account's token).
const dzSecret = "DZ-own-access-TOKEN-0042"

// fakeDZ is a dizqueTV that stores what it is sent and echoes it back, as the real one does.
type fakeDZ struct {
	mu       sync.Mutex
	shows    map[string]map[string]any
	fillers  map[string]map[string]any
	channels map[int]map[string]any
	servers  []map[string]any
	next     int
	seen     []string // "METHOD path"
	heads    []string
}

func newFakeDZ(plexURL string) *fakeDZ {
	return &fakeDZ{
		shows:    map[string]map[string]any{},
		fillers:  map[string]map[string]any{},
		channels: map[int]map[string]any{},
		servers: []map[string]any{
			{"name": "Other", "uri": "http://10.9.9.9:32400", "accessToken": dzSecret},
			{"name": "PlexServer", "uri": plexURL, "accessToken": secret},
		},
	}
}

func (f *fakeDZ) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.Method+" "+r.URL.Path)
	raw, _ := io.ReadAll(r.Body)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	out := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.URL.Path == "/api/channels":
		nums := []int{}
		for n := range f.channels {
			nums = append(nums, n)
		}
		out(nums)
	case r.URL.Path == "/api/plex-servers":
		out(f.servers)
	case r.URL.Path == "/api/channel-tools/time-slots":
		out(map[string]any{"programs": doc["programs"], "startTime": "2026-01-01T00:00:00.000Z"})
	case r.URL.Path == "/api/upload/image":
		out(map[string]any{"status": true, "data": map[string]any{"fileUrl": "http://dz/images/uploads/x.png"}})
	case len(parts) == 4 && parts[1] == "channel" && parts[2] == "programless":
		n, _ := strconv.Atoi(parts[3])
		ch, ok := f.channels[n]
		if !ok {
			w.WriteHeader(404)
			return
		}
		cp := map[string]any{}
		for k, v := range ch {
			if k != "programs" {
				cp[k] = v
			}
		}
		out(cp)
	case len(parts) == 3 && parts[1] == "channel" && r.Method == http.MethodGet:
		n, _ := strconv.Atoi(parts[2])
		ch, ok := f.channels[n]
		if !ok {
			w.WriteHeader(404)
			return
		}
		out(ch)
	case r.URL.Path == "/api/channel" && (r.Method == http.MethodPost || r.Method == http.MethodPut):
		n := int(doc["number"].(float64))
		f.channels[n] = doc
		out(map[string]any{})
	case r.URL.Path == "/api/channel" && r.Method == http.MethodDelete:
		delete(f.channels, int(doc["number"].(float64)))
		out(map[string]any{})
	default:
		for kind, store := range map[string]map[string]map[string]any{"show": f.shows, "filler": f.fillers} {
			switch {
			case r.URL.Path == "/api/"+kind+"s":
				list := []map[string]any{}
				for id, d := range store {
					list = append(list, map[string]any{"id": id, "name": d["name"], "count": 1})
				}
				out(list)
				return
			case r.URL.Path == "/api/"+kind && r.Method == http.MethodPut:
				f.next++
				id := "id" + strconv.Itoa(f.next)
				store[id] = doc
				out(map[string]any{"id": id})
				return
			case len(parts) == 3 && parts[1] == kind:
				switch r.Method {
				case http.MethodGet:
					d, ok := store[parts[2]]
					if !ok {
						w.WriteHeader(404)
						return
					}
					cp := map[string]any{"id": parts[2]}
					for k, v := range d {
						cp[k] = v
					}
					out(cp)
				case http.MethodPost:
					store[parts[2]] = doc
					out(map[string]any{})
				case http.MethodDelete:
					delete(store, parts[2])
					out(map[string]any{})
				}
				return
			}
		}
		w.WriteHeader(404)
	}
}

type dzHarness struct {
	dz      *fakeDZ
	dzSrv   *httptest.Server
	art     *httptest.Server
	door    *Door
	plex    Upstream
	relayOn bool
	answers []string
}

func newDZHarness(t *testing.T) *dzHarness {
	t.Helper()
	h := &dzHarness{}
	h.plex = Upstream{Enabled: true, URL: "http://192.168.1.4:32400", Secret: secret}
	h.dz = newFakeDZ(h.plex.URL)
	h.dzSrv = httptest.NewServer(h.dz)
	t.Cleanup(h.dzSrv.Close)
	h.art = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.dz.mu.Lock()
		h.dz.heads = append(h.dz.heads, r.Method+" "+r.URL.Path)
		h.dz.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/missing.png") {
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(h.art.Close)
	h.door = New(Config{
		Settings: func(context.Context) (Settings, error) {
			s := DefaultSettings()
			s.Enabled = h.relayOn // the relay switch is a node's: a home caller is served anyway
			return s, nil
		},
		Upstream: func(_ context.Context, ext *Extension) (Upstream, error) {
			if ext.Name == "dizquetv" {
				return Upstream{Enabled: true, URL: h.dzSrv.URL}, nil
			}
			return h.plex, nil
		},
		ArtBase: func(context.Context) (string, error) { return h.art.URL + "/logos", nil },
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return h
}

var dzHome = Caller{Kind: "home", WorkerID: "tv.maktaba.dizquetv", Session: "inv-1"}

// do serves one call as the home's dizqueTV plugin and keeps every answer it got.
func (h *dzHarness) do(t *testing.T, method, target string, body any) (Result, string) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	sink := &bufSink{}
	res := h.door.Serve(context.Background(), dzHome, Request{Ext: "dizquetv", Method: method, Target: target,
		Accept: "application/json", ContentType: "application/json", Body: raw}, sink)
	h.answers = append(h.answers, sink.String())
	if res.Err != nil {
		h.answers = append(h.answers, res.Err.Detail)
	}
	return res, sink.String()
}

func (h *dzHarness) ok(t *testing.T, method, target string, body any) string {
	t.Helper()
	res, out := h.do(t, method, target, body)
	if res.Err != nil || res.Status >= 300 {
		t.Fatalf("%s %s: %+v %s", method, target, res, out)
	}
	return out
}

// leakForms is every form of a secret a plugin must never see: the secret, its URL-encoded
// forms, and every part of it eight characters long or more.
func leakForms(s string) []string {
	forms := []string{s, url.QueryEscape(s), url.PathEscape(s), strings.ToLower(s)}
	for i := 0; i+8 <= len(s); i++ {
		forms = append(forms, s[i:i+8])
	}
	return forms
}

func assertNoSecret(t *testing.T, where, got string) {
	t.Helper()
	for _, s := range []string{secret, dzSecret} {
		for _, f := range leakForms(s) {
			if strings.Contains(got, f) {
				t.Fatalf("%s carries a form of a token (%q): %s", where, f, got)
			}
		}
	}
	if strings.Contains(strings.ToLower(got), "x-plex-token") {
		t.Fatalf("%s carries a token parameter: %s", where, got)
	}
	if strings.Contains(got, "192.168.1.4") {
		t.Fatalf("%s carries Plex's address: %s", where, got)
	}
}

// spec is a token-free programme record, as the plugin's program_spec builds it.
func spec(rk string) map[string]any {
	return map[string]any{
		"title": "الحلقة " + rk, "key": "/library/metadata/" + rk, "ratingKey": rk,
		"icon": "/library/metadata/100/thumb/1700000000", "type": "episode",
		"duration": 1500000, "durationStr": "00:25:00.0", "summary": "", "rating": nil,
		"date": "2024-01-01", "year": 2024, "plexFile": "/library/parts/" + rk + "/1700000000/file.mp4",
		"file": "/volume1/share/Show/e" + rk + ".mp4", "showTitle": "Show", "season": 1, "episode": 1,
		"commercials": []any{},
	}
}

// The calls the dizqueTV stages make through the door are allowed for the home's own plugin
// only; the raw reads and the raw programme writes never are.
func TestDizqueTVAllowList(t *testing.T) {
	allowed := [][2]string{
		{"GET", "/maktaba/channels"},
		{"GET", "/maktaba/shows"},
		{"GET", "/maktaba/fillers"},
		{"GET", "/maktaba/plex-servers"},
		{"GET", "/maktaba/show/0b5e3c1e-6f6a-4c1f-9d0e-1a2b3c4d5e6f"},
		{"GET", "/maktaba/filler/id7"},
		{"GET", "/maktaba/channel/5"},
		{"GET", "/maktaba/channel/programless/5"},
		{"PUT", "/maktaba/show"},
		{"POST", "/maktaba/show/id1"},
		{"PUT", "/maktaba/filler"},
		{"POST", "/maktaba/filler/id1"},
		{"POST", "/maktaba/channel"},
		{"PUT", "/maktaba/channel"},
		{"HEAD", "/maktaba/art?url=http%3A%2F%2Flogos.example%2Fa.png"},
		{"POST", "/api/channel-tools/time-slots"},
		{"POST", "/api/upload/image"},
		{"DELETE", "/api/show/id1"},
		{"DELETE", "/api/filler/id1"},
		{"DELETE", "/api/channel"},
	}
	for _, c := range allowed {
		if _, _, _, err := DizqueTV.MatchFor(true, c[0], c[1]); err != nil {
			t.Errorf("home: %s %s: %v", c[0], c[1], err)
		}
		if _, _, _, err := DizqueTV.MatchFor(false, c[0], c[1]); err == nil {
			t.Errorf("node: %s %s was allowed", c[0], c[1])
		}
	}
	for _, c := range [][2]string{
		// raw reads that answer with programme bodies or a server's token
		{"GET", "/api/show/id1"},
		{"GET", "/api/filler/id1"},
		{"GET", "/api/channel/5"},
		{"GET", "/api/channel/programless/5"},
		{"GET", "/api/plex-servers"},
		{"GET", "/api/channels"},
		{"GET", "/api/shows"},
		// raw writes of bodies that carry programmes
		{"PUT", "/api/show"},
		{"POST", "/api/show/id1"},
		{"PUT", "/api/filler"},
		{"POST", "/api/filler/id1"},
		{"POST", "/api/channel"},
		{"PUT", "/api/channel"},
		// anything else of dizqueTV's
		{"PUT", "/api/plex-servers"},
		{"POST", "/api/settings/plex"},
		{"GET", "/api/xmltv-settings"},
		{"GET", "/maktaba/show/../channel/5"},
		{"GET", "/maktaba/channel/abc"},
		{"GET", "/maktaba/channels?X-Plex-Token=abc"},
		{"HEAD", "/maktaba/art"},
	} {
		if _, _, _, err := DizqueTV.MatchFor(true, c[0], c[1]); err == nil {
			t.Errorf("%s %s was allowed", c[0], c[1])
		}
	}
}

// dizqueTV stays home-only: a node is refused before any rule is looked at, and the Machines
// page still lists it as home-only.
func TestDizqueTVIsNeverRelayedToANode(t *testing.T) {
	h := newDZHarness(t)
	h.relayOn = true
	res := h.door.Serve(context.Background(), node, Request{Ext: "dizquetv", Method: "GET", Target: "/maktaba/channels"}, &bufSink{})
	if res.Err == nil || res.Err.Code != CodeHomeOnly {
		t.Fatalf("a node: %+v", res)
	}
	if _, ok := HomeOnly["dizquetv"]; !ok {
		t.Fatal("dizquetv is no longer listed home-only")
	}
	if len(h.dz.seen) != 0 {
		t.Fatalf("a refused call reached dizqueTV: %v", h.dz.seen)
	}
}

// The stages' whole conversation through the door: a show and a filler list written from
// token-free records, a channel written from references to them, everything read back. dizqueTV
// holds the token in every programme the door built; no answer the plugin got carries it, in
// any form, nor dizqueTV's own copy from its plex-servers, nor Plex's address.
func TestDizqueTVProgrammesAreBuiltOnTheHomeAndTheTokenNeverComesBack(t *testing.T) {
	h := newDZHarness(t)
	// what the user made by hand in dizqueTV's own page: programmes with the token in them
	h.dz.shows["mine"] = map[string]any{"name": "My show", "content": []any{map[string]any{
		"ratingKey": "900", "duration": 60000, "title": "x?X-Plex-Token=" + secret,
		"icon":        "http://192.168.1.4:32400/library/metadata/900/thumb/1?X-Plex-Token=" + url.QueryEscape(secret),
		"episodeIcon": "http://p/?X-Plex-Token=" + dzSecret, "serverKey": "PlexServer", "type": "episode",
	}}}

	servers := h.ok(t, "GET", "/maktaba/plex-servers", nil)
	var sv []map[string]any
	_ = json.Unmarshal([]byte(servers), &sv)
	if len(sv) != 2 || sv[1]["name"] != "PlexServer" || sv[1]["home"] != true || sv[0]["home"] != false {
		t.Fatalf("plex-servers: %s", servers)
	}

	created := h.ok(t, "PUT", "/maktaba/show", map[string]any{"name": "maktaba-plan · Show", "content": []any{spec("101"), spec("102")}})
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &c); err != nil || c.ID == "" {
		t.Fatalf("PUT show: %s", created)
	}
	stored := h.dz.shows[c.ID]["content"].([]any)[0].(map[string]any)
	if stored["icon"] != "http://192.168.1.4:32400/library/metadata/100/thumb/1700000000?X-Plex-Token="+secret {
		t.Fatalf("the door did not build the icon: %v", stored["icon"])
	}
	if stored["serverKey"] != "PlexServer" {
		t.Fatalf("the door did not name the server: %v", stored["serverKey"])
	}
	h.ok(t, "POST", "/maktaba/show/"+c.ID, map[string]any{"name": "maktaba-plan · Show", "content": []any{spec("102"), spec("101")}})
	fill := h.ok(t, "PUT", "/maktaba/filler", map[string]any{"name": "maktaba-plan · Clips", "mode": "custom", "content": []any{spec("201")}})
	var fc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(fill), &fc)

	show := h.ok(t, "GET", "/maktaba/show/"+c.ID, nil)
	var sd struct {
		Name    string           `json:"name"`
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal([]byte(show), &sd); err != nil || len(sd.Content) != 2 || sd.Content[0]["ratingKey"] != "102" ||
		sd.Content[0]["duration"] != float64(1500000) {
		t.Fatalf("show projection: %s", show)
	}
	h.ok(t, "GET", "/maktaba/show/mine", nil)
	h.ok(t, "GET", "/maktaba/filler/"+fc.ID, nil)
	h.ok(t, "GET", "/maktaba/shows", nil)
	h.ok(t, "GET", "/maktaba/fillers", nil)

	// the channel: the timeline the time-slot tool made from the projections, written by
	// reference; the door fills each programme from the stored show
	prog := func(rk string, order int) map[string]any {
		p := map[string]any{}
		for _, x := range sd.Content {
			if x["ratingKey"] == rk {
				for k, v := range x {
					p[k] = v
				}
			}
		}
		p["customShowId"], p["customShowName"], p["customOrder"] = c.ID, "maktaba-plan · Show", order
		return p
	}
	slots := h.ok(t, "POST", "/api/channel-tools/time-slots", map[string]any{
		"programs": []any{prog("102", 0), prog("101", 1)}, "schedule": map[string]any{"period": 86400000},
	})
	var ts struct {
		Programs  []map[string]any `json:"programs"`
		StartTime string           `json:"startTime"`
	}
	_ = json.Unmarshal([]byte(slots), &ts)
	ts.Programs = append(ts.Programs, map[string]any{"duration": 1000, "isOffline": true})
	body := map[string]any{
		"number": 5, "name": "Channel", "groupTitle": "dizqueTV", "programs": ts.Programs, "startTime": ts.StartTime,
		"fillerCollections":    []any{map[string]any{"id": fc.ID, "weight": 1, "cooldown": 0}},
		"fillerRepeatCooldown": 1800000, "scheduleBackup": map[string]any{"period": 86400000, "slots": []any{}},
		"maktabaPlan": map[string]any{"key": "ch5", "hash": "h"}, "icon": "", "fallback": []any{},
	}
	h.ok(t, "POST", "/maktaba/channel", body)
	ch := h.dz.channels[5]
	p0 := ch["programs"].([]any)[0].(map[string]any)
	if !strings.Contains(p0["icon"].(string), "X-Plex-Token="+secret) || p0["customShowId"] != c.ID ||
		p0["plexFile"] != "/library/parts/102/1700000000/file.mp4" || p0["customOrder"] != float64(0) {
		t.Fatalf("the channel's programme was not filled from the show: %v", p0)
	}
	if off := ch["programs"].([]any)[2].(map[string]any); off["isOffline"] != true || len(off) != 2 {
		t.Fatalf("filler time: %v", off)
	}

	full := h.ok(t, "GET", "/maktaba/channel/5", nil)
	var old struct {
		Programs []map[string]any `json:"programs"`
	}
	_ = json.Unmarshal([]byte(full), &old)
	if len(old.Programs) != 3 || old.Programs[0]["maktabaRef"] != float64(0) || old.Programs[1]["ratingKey"] != "101" {
		t.Fatalf("channel projection: %s", full)
	}
	// a rewrite that keeps what is on air: the aired programme by reference to the stored
	// channel, the rest from the show
	again := map[string]any{}
	for k, v := range body {
		again[k] = v
	}
	again["programs"] = []any{old.Programs[0], prog("101", 1)}
	h.ok(t, "PUT", "/maktaba/channel", again)
	if got := h.dz.channels[5]["programs"].([]any); len(got) != 2 || got[0].(map[string]any)["ratingKey"] != "102" {
		t.Fatalf("rewrite: %v", got)
	}
	h.ok(t, "GET", "/maktaba/channel/programless/5", nil)
	h.ok(t, "GET", "/maktaba/channels", nil)
	h.ok(t, "POST", "/api/upload/image", nil)
	h.ok(t, "DELETE", "/api/channel", map[string]any{"number": 5})
	h.ok(t, "DELETE", "/api/filler/"+fc.ID, nil)

	for i, a := range h.answers {
		assertNoSecret(t, "answer "+strconv.Itoa(i), a)
	}
}

// A secret stored in a channel's own settings (a logo picked from Plex) is hidden from the
// plugin, and a write the plugin makes from that projection keeps the stored value.
func TestDizqueTVHiddenChannelFieldsSurviveARewrite(t *testing.T) {
	h := newDZHarness(t)
	logo := "http://192.168.1.4:32400/photo?url=x&X-Plex-Token=" + secret
	h.dz.channels[7] = map[string]any{
		"number": 7, "name": "Mine", "icon": logo, "offlinePicture": "http://p/?" + url.QueryEscape("X-Plex-Token="+secret),
		"fallback": []any{map[string]any{"ratingKey": "1", "icon": logo}}, "transcoding": map[string]any{"targetResolution": ""},
		"customField": "kept", "programs": []any{}, "startTime": "2026-01-01T00:00:00.000Z",
		"maktabaPlan": map[string]any{"key": "ch7"},
	}
	ex := h.ok(t, "GET", "/maktaba/channel/programless/7", nil)
	assertNoSecret(t, "programless", ex)
	var doc map[string]any
	_ = json.Unmarshal([]byte(ex), &doc)
	for _, k := range []string{"icon", "offlinePicture", "fallback", "customField", "programs"} {
		if _, ok := doc[k]; ok {
			t.Fatalf("programless shows %s: %s", k, ex)
		}
	}
	doc["name"], doc["programs"] = "Renamed", []any{}
	h.ok(t, "PUT", "/maktaba/channel", doc)
	got := h.dz.channels[7]
	if got["name"] != "Renamed" || got["icon"] != logo || got["customField"] != "kept" || len(got["fallback"].([]any)) != 1 {
		t.Fatalf("the rewrite lost a hidden field: %v", got)
	}
}

func TestDizqueTVWriteRefusals(t *testing.T) {
	h := newDZHarness(t)
	h.dz.shows["s1"] = map[string]any{"name": "S", "content": []any{map[string]any{"ratingKey": "1", "duration": 1000}}}
	h.dz.channels[3] = map[string]any{"number": 3, "name": "C", "programs": []any{map[string]any{"ratingKey": "1", "duration": 1000, "customShowId": "s1"}}}
	bad := func(mut func(m map[string]any)) map[string]any {
		s := spec("101")
		mut(s)
		return map[string]any{"name": "maktaba-plan · X", "content": []any{s}}
	}
	ch := func(progs ...any) map[string]any {
		return map[string]any{"number": 3, "name": "C", "programs": progs}
	}
	for name, c := range map[string]struct {
		method, target string
		body           any
	}{
		"a part that is not Plex's":            {"PUT", "/maktaba/show", bad(func(m map[string]any) { m["plexFile"] = "@evil.example/x" })},
		"a key of another item":                {"PUT", "/maktaba/show", bad(func(m map[string]any) { m["key"] = "/library/metadata/999" })},
		"a field the door does not know":       {"PUT", "/maktaba/show", bad(func(m map[string]any) { m["episodeIcon"] = "http://x" })},
		"a server named by the plugin":         {"PUT", "/maktaba/show", bad(func(m map[string]any) { m["serverKey"] = "Other" })},
		"commercials":                          {"PUT", "/maktaba/show", bad(func(m map[string]any) { m["commercials"] = []any{map[string]any{"x": 1}} })},
		"not a movie or an episode":            {"PUT", "/maktaba/show", bad(func(m map[string]any) { m["type"] = "track" })},
		"a filler mode":                        {"PUT", "/maktaba/filler", map[string]any{"name": "x", "mode": "smart", "content": []any{}}},
		"a programme of no stored show":        {"PUT", "/maktaba/channel", ch(map[string]any{"ratingKey": "2", "duration": 1, "customShowId": "s1"})},
		"a show that is not stored":            {"PUT", "/maktaba/channel", ch(map[string]any{"ratingKey": "1", "duration": 1, "customShowId": "nope"})},
		"a reference past the channel":         {"PUT", "/maktaba/channel", ch(map[string]any{"ratingKey": "1", "duration": 1, "maktabaRef": 4})},
		"a reference to another item":          {"PUT", "/maktaba/channel", ch(map[string]any{"ratingKey": "9", "duration": 1, "maktabaRef": 0})},
		"a programme that plays from Plex":     {"PUT", "/maktaba/channel", ch(map[string]any{"duration": 1, "plexFile": "/library/parts/1/file", "serverKey": "PlexServer"})},
		"a fallback":                           {"PUT", "/maktaba/channel", map[string]any{"number": 3, "name": "C", "programs": []any{}, "fallback": []any{map[string]any{"ratingKey": "1"}}}},
		"a channel key the door does not know": {"PUT", "/maktaba/channel", map[string]any{"number": 3, "name": "C", "programs": []any{}, "plexServer": "x"}},
		"a channel with no number":             {"PUT", "/maktaba/channel", map[string]any{"name": "C", "programs": []any{}}},
	} {
		before := len(h.dz.seen)
		res, _ := h.do(t, c.method, c.target, c.body)
		if res.Err == nil || res.Err.Code != CodeBadRequest {
			t.Errorf("%s: %+v", name, res)
		}
		for _, s := range h.dz.seen[before:] {
			if !strings.HasPrefix(s, "GET ") {
				t.Errorf("%s: a refused write reached dizqueTV: %s", name, s)
			}
		}
	}
	// no Plex token on the home: nothing can be built, so nothing is written
	h.plex.Secret = ""
	h.door.Forget()
	if res, _ := h.do(t, "PUT", "/maktaba/show", map[string]any{"name": "x", "content": []any{spec("1")}}); res.Err == nil ||
		res.Err.Code != CodeNotConfigured {
		t.Fatalf("no token: %+v", res)
	}
}

// The hosted-logo check: a HEAD of a link under the home's own dz_art_url only, answered with
// the status alone.
func TestDizqueTVArtCheck(t *testing.T) {
	h := newDZHarness(t)
	q := func(u string) string { return "/maktaba/art?url=" + url.QueryEscape(u) }
	res, _ := h.do(t, "HEAD", q(h.art.URL+"/logos/a%20b.png"), nil)
	if res.Err != nil || res.Status != 200 {
		t.Fatalf("hosted: %+v", res)
	}
	if res, _ := h.do(t, "HEAD", q(h.art.URL+"/logos/missing.png"), nil); res.Err != nil || res.Status != 404 {
		t.Fatalf("missing: %+v", res)
	}
	for _, u := range []string{
		"http://169.254.169.254/logos/a.png",
		h.art.URL + "/other/a.png",
		h.art.URL + "/logos/../secret.png",
		h.art.URL + "/logos/sub/a.png",
		h.art.URL + "/logos/a.png?x=1",
	} {
		if res, _ := h.do(t, "HEAD", q(u), nil); res.Err == nil {
			t.Errorf("%s was checked: %+v", u, res)
		}
	}
	if len(h.dz.heads) != 2 {
		t.Fatalf("the art host got %v", h.dz.heads)
	}
}

// A request body over the extension's limit is refused before it is read further; dizqueTV's
// limit is its own (a channel's programmes), not the Plex calls' 64 KB.
func TestDizqueTVBodyLimit(t *testing.T) {
	h := newDZHarness(t)
	big := bytes.Repeat([]byte("x"), MaxRequestBody+1)
	res := h.door.Serve(context.Background(), dzHome, Request{Ext: "dizquetv", Method: "POST", Target: "/api/channel-tools/time-slots",
		Body: append(append([]byte(`{"programs":[],"schedule":{},"pad":"`), big...), '"', '}')}, &bufSink{})
	if res.Err != nil {
		t.Fatalf("a large timeline: %+v", res)
	}
	res = h.door.Serve(context.Background(), dzHome, Request{Ext: "dizquetv", Method: "POST", Target: "/api/channel-tools/time-slots",
		Body: make([]byte, DizqueTV.MaxBody+1)}, &bufSink{})
	if res.Err == nil || res.Err.Code != CodeTooLarge {
		t.Fatalf("over the limit: %+v", res)
	}
}
