//go:build parental_bypass

package router_test

// Epic 57 story 57.13 T04 and T05: the bypass rehearsal.
//
// A home is seeded on a real migrated Postgres behind the production router
// (setupHome), and a caller held to 9+ asks every surface for what is above its limit:
// every entry of the surface registry that has a probe (parental.Surfaces), with each
// hidden fixture's id where the probe takes one, and the surfaces a GET cannot ask
// (search, opening a stream, starting a watch, a channel's signed URL, GraphQL, the
// live feeds, a thumbnail by its hash, a play queue made before the limit was
// tightened). An answer leaks when it names a hidden subject (its id, its title, its
// content hash, a word of its transcript) or when a hidden id is answered differently
// from an id that names nothing: another status, another header, another body.
//
// The caller is, in turn: the restricted child; the child signed in through Maktaba
// Cloud; that sign-in forwarded by the relay agent on this machine (X-Maktaba-Via from
// loopback); and an unrestricted account whose device said 9+ (the token's `ac`).
//
// The rehearsal proves itself two ways: the same questions asked by an unrestricted
// viewer must find the hidden subjects (so a probe that could see nothing is noticed),
// and with the filter taken off one surface the rehearsal must fail naming it (T05).
//
// Run: go test -tags parental_bypass -run 'TestParentalBypass' ./internal/router/
// (the CI job `parental-bypass` of _integration.yml).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

const (
	pbBlockedLibTitle = "KidB-Title" // the one video of library B, which a rule blocks for the child
	pbTeenTag         = "zarqa-teen-tag"
)

// pbFixtures is what one caller must not be shown, and what it may.
type pbFixtures struct {
	hidden, shown map[string][]string // by subject kind
	markers       []string
}

// pbHome is the rehearsal's home: setupHome with transcripts, a tag on the teen video,
// library B blocked for the child, and no request of the child's own (it would name
// the video the child itself asked for).
func pbHome(t *testing.T) *p57Home {
	t.Helper()
	h := setupHome(t)
	h.transcripts()
	h.exec(`INSERT INTO tags (id, name, name_norm) VALUES ('00000000-0000-4000-8000-0000573e0102', $1, $1)`, pbTeenTag)
	h.exec(`INSERT INTO video_tags (video_id, tag_id) VALUES ($1, '00000000-0000-4000-8000-0000573e0102')`, pfTeen)
	h.exec(`DELETE FROM parental_requests`)
	h.exec(`INSERT INTO parental_rules (user_id, effect, subject_kind, subject_id) VALUES ($1, 'block', 'library', $2)`, pfChild, pfLibB)
	h.exec(`UPDATE parental_policies SET version = version + 1 WHERE user_id = $1`, pfChild)
	h.cache.Invalidate("")
	// a route that streams (a feed asked as a plain request, a live channel) ends itself
	h.stream = 400 * time.Millisecond
	return h
}

func (h *p57Home) hashes(ids ...string) []string {
	h.t.Helper()
	var out []string
	for _, id := range ids {
		var hash string
		if err := h.db.QueryRow(`SELECT content_hash FROM videos WHERE id = $1`, id).Scan(&hash); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, hash)
	}
	return out
}

// fixtures is what who must not see. rules says the caller is the child (its block
// rule on library B holds); a sign-in limited only by its device has no rules.
func (h *p57Home) fixtures(rules bool) pbFixtures {
	f := pbFixtures{
		hidden: map[string][]string{
			parental.SubjectVideo:      {pfTeen, pfTeen2, pfUnrated},
			parental.SubjectSeries:     {pfSeries16},
			parental.SubjectCollection: {pfCol16},
			parental.SubjectChannel:    {phCh16},
		},
		shown: map[string][]string{
			parental.SubjectVideo:      {pfKid},
			parental.SubjectSeries:     {pfSeries},
			parental.SubjectCollection: {pfCol},
			parental.SubjectLibrary:    {pfLibA},
			parental.SubjectChannel:    {phCh9},
		},
		markers: []string{pfTeen, pfTeen2, pfUnrated, pfSeries16, pfCol16, phCh16,
			pfTeenName, "Late-Episode-Title", "Unrated-Title", "Pf late show", "Pf late picks", phCh16Name, phProgramme,
			"teen-secret", "unrated-secret", pbTeenTag},
	}
	f.markers = append(f.markers, h.hashes(pfTeen, pfTeen2, pfUnrated)...)
	if rules {
		f.hidden[parental.SubjectVideo] = append(f.hidden[parental.SubjectVideo], pfKidB)
		f.hidden[parental.SubjectLibrary] = []string{pfLibB}
		f.markers = append(f.markers, pfKidB, pbBlockedLibTitle, pfLibB, "PfBeta")
		f.markers = append(f.markers, h.hashes(pfKidB)...)
	} else {
		f.shown[parental.SubjectLibrary] = append(f.shown[parental.SubjectLibrary], pfLibB)
	}
	return f
}

var (
	pbPlaceholder = regexp.MustCompile(`\{(video|series|collection|library|channel)\}`)
	pbParam       = regexp.MustCompile(`\{[^}]+\}`)
)

// pbRun is one caller's rehearsal: what leaked, and how much was asked.
type pbRun struct {
	h      *p57Home
	who    string
	fx     pbFixtures
	leaks  []string
	probes int // registry entries asked
	asked  int // requests made
}

func (r *pbRun) leak(surface, format string, args ...any) {
	r.leaks = append(r.leaks, surface+": "+fmt.Sprintf(format, args...))
}

// scan reports every marker body names.
func (r *pbRun) scan(surface, body string) {
	for _, m := range r.fx.markers {
		if strings.Contains(body, m) {
			r.leak(surface, "the answer names %s", m)
		}
	}
}

func (r *pbRun) do(method, path, body string) *httptest.ResponseRecorder {
	r.asked++
	return r.h.raw(r.who, method, path, body)
}

// ask sends one request and scans its answer.
func (r *pbRun) ask(surface, method, path, body string) *httptest.ResponseRecorder {
	rec := r.do(method, path, body)
	if rec.Code >= 500 && rec.Code != http.StatusServiceUnavailable {
		r.leak(surface, "%s %s answered %d: the probe did not run: %s", method, path, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	r.scan(surface, rec.Body.String())
	return rec
}

// alike asks the same thing of a hidden id and of an id that names nothing: the two
// answers must be one (status, headers, body, once each id is written the same way).
func (r *pbRun) alike(surface, method, pathFmt, bodyFmt, hidden string) {
	missing := uuid.NewString()
	a := r.do(method, strings.ReplaceAll(pathFmt, "{id}", hidden), strings.ReplaceAll(bodyFmt, "{id}", hidden))
	b := r.do(method, strings.ReplaceAll(pathFmt, "{id}", missing), strings.ReplaceAll(bodyFmt, "{id}", missing))
	norm := func(rec *httptest.ResponseRecorder, id string) string {
		hd := rec.Header().Clone()
		for _, k := range []string{"X-Request-Id", "Date", "Traceparent"} {
			hd.Del(k)
		}
		var hb bytes.Buffer
		_ = hd.Write(&hb)
		body := pfRequestID.ReplaceAllString(rec.Body.String(), `"requestId":"R"`)
		return fmt.Sprintf("%d\n%s\n%s", rec.Code, strings.ReplaceAll(hb.String(), id, "ID"), strings.ReplaceAll(body, id, "ID"))
	}
	if x, y := norm(a, hidden), norm(b, missing); x != y {
		r.leak(surface, "%s %s: the hidden id %s answers\n%s\nan id that names nothing answers\n%s", method, pathFmt, hidden, x, y)
	}
	// the hidden id is in the question, so only the other markers are looked for
	body := strings.ReplaceAll(a.Body.String(), hidden, "")
	r.scan(surface, body)
}

// registry asks every entry of the surface registry that has a probe.
func (r *pbRun) registry() {
	for _, s := range parental.Surfaces {
		if s.Probe == "" {
			continue
		}
		r.probes++
		if strings.HasPrefix(s.Probe, "/ws/") {
			r.feed(s)
			continue
		}
		m := pbPlaceholder.FindStringSubmatch(s.Probe)
		if m == nil {
			r.ask(s.Probe, http.MethodGet, pbParam.ReplaceAllString(s.Probe, "x"), "")
			continue
		}
		kind := m[1]
		// the other parameters of the path name nothing in particular
		path := pbParam.ReplaceAllString(strings.Replace(s.Probe, m[0], "\x00", 1), "x")
		path = strings.Replace(path, "\x00", "{id}", 1)
		for _, id := range r.fx.hidden[kind] {
			r.alike(s.Probe, http.MethodGet, path, "", id)
		}
		for _, id := range r.fx.shown[kind] {
			r.ask(s.Probe, http.MethodGet, strings.Replace(path, "{id}", id, 1), "")
		}
	}
}

// feed is a live feed's probe: events naming hidden subjects are published, then one
// every account is sent, and what arrived is scanned.
func (r *pbRun) feed(s parental.Surface) {
	h := r.h
	var path, channel string
	switch s.Pattern {
	case "/ws/home":
		path, channel = "/ws/home", ws.HomeChannel
	case "/ws/library/{id}":
		for _, id := range r.fx.hidden[parental.SubjectLibrary] {
			r.alike(s.Probe, http.MethodGet, "/ws/library/{id}", "", id)
		}
		path, channel = "/ws/library/"+pfLibA, "library:"+pfLibA
	default:
		// a feed about one video: a hidden one answers as one that is not there
		for _, id := range r.fx.hidden[parental.SubjectVideo] {
			r.alike(s.Probe, http.MethodGet, strings.Replace(pbPlaceholder.ReplaceAllString(s.Probe, "{id}"), "{video_id}", "{id}", 1), "", id)
		}
		return
	}
	r.asked++
	f := h.feed(r.who, path, channel)
	for _, ev := range []struct {
		typ     string
		payload map[string]any
	}{
		{"video.added", map[string]any{"video_id": pfTeen, "title": pfTeenName}},
		{"video.added", map[string]any{"video": map[string]any{"id": pfUnrated, "title": "Unrated-Title"}}},
		{"videos.changed", map[string]any{"video_ids": []any{pfKid, pfTeen2}}},
		{"series.changed", map[string]any{"series_id": pfSeries16, "name": "Pf late show"}},
		{"collection.changed", map[string]any{"collection_id": pfCol16, "name": "Pf late picks"}},
		{"something.new", map[string]any{"note": pfTeenName}},
	} {
		h.publish(channel, ev.typ, ev.payload)
	}
	tag := "pb-mark-" + uuid.NewString()
	h.publish(channel, ws.EventCapabilitiesChanged, map[string]any{"etag": tag})
	if !f.has(tag) {
		r.leak(s.Probe, "the feed never got the closing event: %s", f.body())
	}
	r.scan(s.Probe, f.close())
}

// attempts are the surfaces a GET probe cannot ask.
func (r *pbRun) attempts() {
	fx := r.fx
	// A3: search by title and by transcript, its pages and one hidden video's passages
	rec := r.ask("POST /api/search", http.MethodPost, "/api/search", `{"q":"zarqa"}`)
	var found struct {
		SearchID string `json:"search_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &found)
	if rec.Code != http.StatusOK || found.SearchID == "" {
		r.leak("POST /api/search", "the search did not run: %d %s", rec.Code, rec.Body.String())
	} else {
		for _, list := range []string{"titles", "transcripts"} {
			r.ask("GET /api/search/results/{sid}", http.MethodGet, "/api/search/results/"+found.SearchID+"?list="+list, "")
		}
		r.ask("GET /api/search/recent", http.MethodGet, "/api/search/recent", "")
		for _, id := range fx.hidden[parental.SubjectVideo] {
			r.alike("GET /api/search/passages", http.MethodGet, "/api/search/passages?sid="+found.SearchID+"&video_id={id}", "", id)
		}
	}
	r.ask("GET /api/search/suggest", http.MethodGet, "/api/search/suggest?q=zar", "")
	for _, id := range fx.hidden[parental.SubjectVideo] {
		// A9: a stream, a watch, a mark, a queue for a hidden video
		r.alike("POST /api/stream/sessions", http.MethodPost, "/api/stream/sessions", `{"video_id":"{id}"}`, id)
		r.alike("POST /api/watch/start", http.MethodPost, "/api/watch/start", `{"video_id":"{id}"}`, id)
		r.alike("POST /api/watch/heartbeat", http.MethodPost, "/api/watch/heartbeat", `{"video_id":"{id}","position_sec":5}`, id)
		r.alike("POST /api/me/favorites", http.MethodPost, "/api/me/favorites", `{"video_id":"{id}"}`, id)
		r.alike("POST /api/me/play-queues", http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"video","id":"{id}"}}`, id)
		// A7: GraphQL
		r.alike("POST /graphql video", http.MethodPost, "/graphql", `{"query":"{ video(id: \"{id}\") { id title } }"}`, id)
	}
	// A2: a thumbnail by its content hash (the hash is no secret, the picture is)
	for _, hash := range r.h.hashes(fx.hidden[parental.SubjectVideo]...) {
		a := r.do(http.MethodGet, "/api/thumbnails/"+hash+"/small.jpg", "")
		b := r.do(http.MethodGet, "/api/thumbnails/"+strings.Repeat("0", len(hash))+"/small.jpg", "")
		if a.Code != b.Code || a.Body.Len() != b.Body.Len() {
			r.leak("GET /api/thumbnails/{hash}/small.jpg", "a hidden video's hash answers %d (%d bytes), an unknown hash %d (%d bytes)",
				a.Code, a.Body.Len(), b.Code, b.Body.Len())
		}
	}
	r.ask("POST /graphql videos", http.MethodPost, "/graphql", `{"query":"{ videos(limit: 50) { items { id title } } }"}`)
	r.ask("POST /graphql collections", http.MethodPost, "/graphql", `{"query":"{ collections { id name } }"}`)
	for _, id := range fx.hidden[parental.SubjectCollection] {
		r.alike("POST /graphql collection", http.MethodPost, "/graphql", `{"query":"{ collection(id: \"{id}\") { id name } }"}`, id)
	}
	// A10, A11: a hidden channel's signed URL and whether it may be watched
	for _, id := range fx.hidden[parental.SubjectChannel] {
		r.alike("POST /api/channels/{id}/play", http.MethodPost, "/api/channels/{id}/play", "{}", id)
	}
	// A17: what a limited caller may not do at all
	for _, x := range []struct{ method, path string }{
		{http.MethodPost, "/api/me/tokens"}, {http.MethodGet, "/api/search/youtube?q=zarqa"},
		{http.MethodGet, "/api/channels/xmltv"}, {http.MethodGet, "/api/channels/playlist.m3u"},
		{http.MethodGet, "/api/plugins"}, {http.MethodGet, "/api/users"}, {http.MethodGet, "/api/parental/review"},
		{http.MethodGet, "/api/parental/settings"},
	} {
		rec := r.ask(x.method+" "+x.path, x.method, x.path, "{}")
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"restricted"`) {
			r.leak(x.method+" "+x.path, "answered %d, want 403 `restricted`: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// own are the attempts only the restricted account itself can make: A5 (a play queue
// made before the limit was tightened) and A15 (changing its own policy, naming itself
// a guardian, answering a request).
func (r *pbRun) own() {
	h := r.h
	h.exec(`UPDATE parental_policies SET max_tier = 18, version = version + 1 WHERE user_id = $1`, pfChild)
	h.cache.Invalidate("")
	made := r.do(http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"library","id":"`+pfLibA+`"}}`)
	if made.Code != http.StatusCreated || !strings.Contains(made.Body.String(), pfTeen) {
		r.leak("A5", "the queue under an 18+ limit was not made with the 16+ video: %d %s", made.Code, made.Body.String())
	}
	h.exec(`UPDATE parental_policies SET max_tier = 9, version = version + 1 WHERE user_id = $1`, pfChild)
	h.cache.Invalidate("")
	var q struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(made.Body.Bytes(), &q)
	r.ask("A5 GET /api/me/play-queues/current", http.MethodGet, "/api/me/play-queues/current", "")
	if q.ID != "" {
		r.ask("A5 GET /api/me/play-queues/{id}", http.MethodGet, "/api/me/play-queues/"+q.ID, "")
	}
	h.exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ($1, $2, 'series', $3)`, phRequest, pfChild, pfSeries)
	for _, x := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/users/" + pfChild + "/parental", `{"max_tier":18,"unrated":"tier_0","allow_only":false,"version":3}`},
		{http.MethodDelete, "/api/users/" + pfChild + "/parental", ""},
		{http.MethodPost, "/api/users/" + pfChild + "/parental/guardians", `{"guardian_id":"` + pfChild + `"}`},
		{http.MethodPost, "/api/users/" + pfChild + "/parental/rules", `{"effect":"allow","subject_kind":"video","subject_id":"` + pfTeen + `"}`},
		{http.MethodDelete, "/api/users/" + pfChild + "/parental/rules/" + phRule, ""},
		{http.MethodPost, "/api/parental/requests/" + phRequest + "/approve", "{}"},
		{http.MethodPost, "/api/parental/users/" + pfChild + "/time-grants", `{"minutes":600}`},
		{http.MethodPut, "/api/parental/users/" + pfChild + "/time-rules", `{"rules":[]}`},
		{http.MethodPut, "/api/parental/settings", `{"enabled":false}`},
		{http.MethodPut, "/api/videos/" + pfTeen + "/age-tier", `{"tier":0}`},
		{http.MethodPut, "/api/libraries/" + pfLibA + "/age-tier", `{"tier":0}`},
	} {
		if rec := r.do(x.method, x.path, x.body); rec.Code/100 == 2 {
			r.leak("A15 "+x.method+" "+x.path, "the restricted account was answered %d: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	h.exec(`DELETE FROM parental_requests`)
	var tier, rules int
	if err := h.db.QueryRow(`SELECT max_tier, (SELECT count(*) FROM parental_rules WHERE user_id = $1 AND effect = 'allow')
		FROM parental_policies WHERE user_id = $1`, pfChild).Scan(&tier, &rules); err != nil || tier != 9 || rules != 0 {
		r.leak("A15", "after the attempts the child's limit is %d with %d allow rules (%v)", tier, rules, err)
	}
}

func rehearse(h *p57Home, who string, fx pbFixtures, own bool) *pbRun {
	r := &pbRun{h: h, who: who, fx: fx}
	r.registry()
	r.attempts()
	if own {
		r.own()
	}
	sort.Strings(r.leaks)
	return r
}

// pbCallers are the callers held to 9+.
var pbCallers = []struct {
	who        string
	rules, own bool
}{
	{"child", true, true},
	{"child-cloud", true, true},
	{"child-relay", true, true},
	{"viewer-device", false, false},
}

// T04: no caller held to 9+ is shown anything above it, on any surface.
func TestParentalBypass(t *testing.T) {
	h := pbHome(t)
	want := 0
	for _, s := range parental.Surfaces {
		if s.Probe != "" {
			want++
		}
	}
	if want < 40 {
		t.Fatalf("the registry has only %d probes", want)
	}
	for _, c := range pbCallers {
		r := rehearse(h, c.who, h.fixtures(c.rules), c.own)
		if r.probes != want {
			t.Errorf("%s: %d of the registry's %d probes were asked", c.who, r.probes, want)
		}
		for _, l := range r.leaks {
			t.Errorf("%s: %s", c.who, l)
		}
		t.Logf("%s: %d registry probes, %d requests, %d leaks", c.who, r.probes, r.asked, len(r.leaks))
	}
}

// The rehearsal can see: the same questions asked by an account nothing limits find the
// hidden subjects on every kind of surface. It is also story 57.12's rule that nothing
// becomes restricted by itself: a viewer with no policy is shown all of it.
func TestParentalBypassSeesWhatIsThere(t *testing.T) {
	h := pbHome(t)
	fx := h.fixtures(false)
	r := &pbRun{h: h, who: "viewer", fx: fx}
	r.registry()
	r.attempts()
	seen := map[string]bool{}
	for _, l := range r.leaks {
		surface, _, _ := strings.Cut(l, ": ")
		seen[surface] = true
	}
	for _, surface := range []string{
		"/api/videos", "/api/videos/{video}", "/api/videos/{video}/segments", "/api/series", "/api/series/{series}", "/api/series/{series}/episodes",
		"/api/collections", "/api/collections/{collection}", "/api/collections/{collection}/items", "/api/home",
		"/api/tags", "/api/channels", "/api/channels/guide", "/api/channels/now", "/api/channels/{channel}", "/api/channels/{channel}/access",
		"/ws/home", "/ws/library/{library}", "/ws/playback/{video}",
		"POST /api/search", "GET /api/search/results/{sid}", "GET /api/search/passages", "POST /api/stream/sessions", "POST /api/watch/start",
		"POST /graphql video", "POST /graphql videos", "POST /graphql collection", "POST /api/channels/{id}/play",
		"POST /api/me/tokens", "GET /api/channels/xmltv",
	} {
		if !seen[surface] {
			t.Errorf("%s shows an unrestricted viewer nothing of the hidden fixtures: the rehearsal could not tell a leak there", surface)
		}
	}
	if len(seen) < 45 {
		t.Errorf("only %d surfaces show an unrestricted viewer the hidden fixtures", len(seen))
	}
	t.Logf("an unrestricted viewer: %d surfaces show the fixtures", len(seen))
}

// T05: with the filter taken off one surface the rehearsal fails, naming it. The filter
// is taken off by answering that surface as the unrestricted viewer would be answered.
func TestParentalBypassCatchesAMissingFilter(t *testing.T) {
	h := pbHome(t)
	viewer := &principal.Principal{UserID: pfViewer, Libraries: []string{pfLibA, pfLibB}, Source: principal.SourceJWT}
	for _, c := range []struct{ name, method, prefix, surface string }{
		{"a list", http.MethodGet, "/api/videos", "/api/videos"},
		{"an object by id", http.MethodGet, "/api/series/", "/api/series/{series}/episodes"},
		{"search", http.MethodPost, "/api/search", "POST /api/search"},
		{"GraphQL", http.MethodPost, "/graphql", "POST /graphql videos"},
		{"opening a stream", http.MethodPost, "/api/stream/sessions", "POST /api/stream/sessions"},
		{"a live feed", http.MethodGet, "/ws/home", "/ws/home"},
		{"live TV", http.MethodGet, "/api/channels", "/api/channels"},
	} {
		h.as = func(req *http.Request, p *principal.Principal) *principal.Principal {
			exact := req.URL.Path == c.prefix || (strings.HasSuffix(c.prefix, "/") && strings.HasPrefix(req.URL.Path, c.prefix))
			if p != nil && p.UserID == pfChild && req.Method == c.method && exact {
				return viewer
			}
			return p
		}
		r := rehearse(h, "child", h.fixtures(true), false)
		named := false
		for _, l := range r.leaks {
			if strings.HasPrefix(l, c.surface+": ") {
				named = true
			}
		}
		if !named {
			t.Errorf("with the filter off %s (%s %s) the rehearsal names no leak on %s; it found: %v", c.name, c.method, c.prefix, c.surface, r.leaks)
		}
		h.as = nil
	}
}
