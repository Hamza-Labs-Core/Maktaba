//go:build parental_bypass

package router_test

// Epic 57 story 57.13 T04 and T05: the bypass rehearsal.
//
// A home is seeded on a real migrated Postgres behind the production router
// (setupHome), and a caller held to 9+ asks every surface for what is above its limit:
// every content route the home mounts (a mounted route whose entry in parental.Surfaces
// is Filtered or ByID: reads and writes alike), each with a hidden fixture where the
// route takes one, and the surfaces that are no content route (GraphQL, what a limited
// caller may not do at all). An answer leaks when it names a hidden subject (its id, its
// title, its content hash, a word of its transcript) or when a hidden id is answered
// differently from an id that names nothing: another status, another header, another body.
//
// A content route the rehearsal cannot reach with a real fixture is a failure too. A path
// parameter no fixture fills, a write with no probe, a content entry of the registry the
// home does not mount and nothing excuses: each fails, naming the route. (The rehearsal
// once sent a path's other parameters as "x". GET /api/libraries/{id}/collections/{kind}/{gid}
// answered "not found" for the kind "x", that counted as asked, and the one unfiltered
// query of the browse handlers went unseen behind it.)
//
// The caller is, in turn: the restricted child; the child signed in through Maktaba
// Cloud; that sign-in forwarded by the relay agent on this machine (X-Maktaba-Via from
// loopback); and an unrestricted account whose device said 9+ (the token's `ac`).
//
// The rehearsal proves itself three ways: the same questions asked by an unrestricted
// viewer must find the hidden subjects on every content route (but the few it explains
// one by one, and the ones that refuse a viewer by role), so a probe that could see
// nothing is noticed; with the filter taken off one surface the rehearsal must fail naming
// it (T05); and with a route's probe or fixture taken away it must fail on that route.
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
	pfTopicB          = "00000000-0000-4000-8000-0000573e0205" // a topic collection of library B
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
	h.groups() // the pipeline's collections: a topic and a clip group with hidden members, and one of each with nothing else
	h.smart()  // saved filters, two of them listing only what is hidden; the teen video's tag
	h.exec(`INSERT INTO topic_collections (id, library_id, playlist_id, name, membership_hash) VALUES ($1, $2, 'PLpfB', 'Pf topic B', 'h')`, pfTopicB, pfLibB)
	h.exec(`INSERT INTO topic_items (topic_id, video_id, position) VALUES ($1, $2, 1)`, pfTopicB, pfKidB)
	// what the accounts watched before any limit: history rows
	for _, u := range []string{pfChild, pfViewer} {
		h.exec(`INSERT INTO watch_sessions (id, user_id, video_id, started_at, duration_sec, percent_complete, state) VALUES
			(gen_random_uuid(), $1, $2, now() - interval '2 hours', 60, 50, 'stopped'),
			(gen_random_uuid(), $1, $3, now() - interval '1 hour', 40, 30, 'stopped')`, u, pfKid, pfTeen)
		h.exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at) VALUES ($1, $2, 33, false, now()), ($1, $3, 10, false, now())`, u, pfTeen, pfKid)
	}
	// the admin's pick for the home's featured video is a 16+ one
	h.exec(`INSERT INTO app_settings (key, value) VALUES ('home.featured', jsonb_build_object('video_id', $1::text))
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, pfTeen)
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
			parental.SubjectCollection: {pfCol16, pfSmartLate, pfSmartLateOld},
			parental.SubjectChannel:    {phCh16},
		},
		shown: map[string][]string{
			parental.SubjectVideo:      {pfKid},
			parental.SubjectSeries:     {pfSeries},
			parental.SubjectCollection: {pfCol, pfSmartOpen, pfSmartOpenOld},
			parental.SubjectLibrary:    {pfLibA},
			parental.SubjectChannel:    {phCh9},
		},
		markers: []string{pfTeen, pfTeen2, pfUnrated, pfSeries16, pfCol16, phCh16,
			pfTeenName, "Late-Episode-Title", "Unrated-Title", "Pf late show", "Pf late picks", phCh16Name, phProgramme,
			"teen-secret", "unrated-secret", pbTeenTag, pfTeenTag,
			pfTopic16, pfTopic16Name, pfClip16, pfClip16Name, pfSmartLate, pfSmartLateOld, pfSmartLateName, pfSmartOldName},
	}
	f.markers = append(f.markers, h.hashes(pfTeen, pfTeen2, pfUnrated)...)
	if rules {
		f.hidden[parental.SubjectVideo] = append(f.hidden[parental.SubjectVideo], pfKidB)
		f.hidden[parental.SubjectLibrary] = []string{pfLibB}
		f.markers = append(f.markers, pfKidB, pbBlockedLibTitle, pfLibB, "PfBeta", pfTopicB, "Pf topic B")
		f.markers = append(f.markers, h.hashes(pfKidB)...)
	} else {
		f.shown[parental.SubjectLibrary] = append(f.shown[parental.SubjectLibrary], pfLibB)
	}
	return f
}

var (
	pbPlaceholder = regexp.MustCompile(`\{(video|series|collection|library|channel)\}`)
	pbParam       = regexp.MustCompile(`\{([^}:]+)(:[^}]*)?\}`)
)

// pbRoute is one content route of the home: a mounted route whose registry entry says it
// returns content (Filtered or ByID). Every one of them must be probed.
type pbRoute struct {
	method, pattern string
	s               parental.Surface
}

func (rt pbRoute) key() string { return rt.method + " " + rt.pattern }

// contentRoutes are the home's content routes: reads first, then writes, removals last (a
// removal must not take a fixture a later probe reads).
func (h *p57Home) contentRoutes() []pbRoute {
	var out []pbRoute
	for _, k := range h.routes {
		m, p, _ := strings.Cut(k, " ")
		if len(p) > 1 {
			p = strings.TrimSuffix(p, "/")
		}
		s, ok := parental.Surfaces.Lookup(m, p)
		if !ok || (s.Class != parental.Filtered && s.Class != parental.ByID) {
			continue
		}
		out = append(out, pbRoute{m, p, s})
	}
	rank := func(m string) int {
		switch m {
		case http.MethodGet:
			return 0
		case http.MethodDelete:
			return 2
		}
		return 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := rank(out[i].method), rank(out[j].method); a != b {
			return a < b
		}
		return out[i].key() < out[j].key()
	})
	return out
}

// pbNotMounted are the registry's content entries the rehearsal's home does not mount, each
// with where it is tested instead. An entry that is mounted may not be listed (the
// rehearsal fails), so a route cannot be excused from its probe here.
var pbNotMounted = map[string]string{
	"/api/panels":                            "MountP54 needs the components service; api/internal/plugins/panels_test.go and p54_plugins_test.go ask it as a restricted account",
	"/api/panels/image":                      "as /api/panels",
	"/api/tv/remote/tune":                    "remote viewing needs a stream provider; api/internal/tvremote and handlers/tv tests ask the same LiveTV check",
	"/api/tv/remote/sessions/{id}":           "as /api/tv/remote/tune",
	"/api/tv/remote/sessions/{id}/heartbeat": "as /api/tv/remote/tune",
}

// pbRun is one caller's rehearsal: what leaked, and what was asked.
type pbRun struct {
	h     *p57Home
	who   string
	fx    pbFixtures
	leaks []string
	asked int // requests made
	// covered are the content routes ("METHOD pattern") a probe was sent to with a
	// fixture above the limit in the question or in what the route lists
	covered map[string]bool
	cur     string // the content route being probed now

	// the caller's own fixtures, made while nothing limited it (prepare)
	user                          string
	queue, session, watch         string
	playlist, playlist2, searchID string
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
	if r.cur != "" {
		r.covered[r.cur] = true
	}
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
	if a.Code >= 500 && a.Code != http.StatusServiceUnavailable {
		r.leak(surface, "%s %s answered %d: the probe did not run: %s", method, pathFmt, a.Code, strings.TrimSpace(a.Body.String()))
	}
	if x, y := norm(a, hidden), norm(b, missing); x != y {
		r.leak(surface, "%s %s: the hidden id %s answers\n%s\nan id that names nothing answers\n%s", method, pathFmt, hidden, x, y)
	}
	// the hidden id is in the question, so only the other markers are looked for
	body := strings.ReplaceAll(a.Body.String(), hidden, "")
	r.scan(surface, body)
}

// prepare makes the caller's own fixtures while nothing limits it: a play queue and a
// stream session made before the limit was tightened (A5), and playlists that hold what is
// now hidden. A child is loosened to 18+ for it; a sign-in its device limits is the same
// account on a sign-in with no signal.
func (r *pbRun) prepare() {
	h := r.h
	r.covered = map[string]bool{}
	r.user, r.cur = pfViewer, ""
	maker := "viewer"
	child := strings.HasPrefix(r.who, "child")
	if child {
		r.user, maker = pfChild, r.who
		h.exec(`UPDATE parental_policies SET max_tier = 18, version = version + 1 WHERE user_id = $1`, pfChild)
		h.cache.Invalidate("")
	}
	r.playlist, r.playlist2 = uuid.NewString(), uuid.NewString()
	h.exec(`INSERT INTO user_playlists (id, user_id, name) VALUES ($1, $3, 'Pf mine'), ($2, $3, 'Pf mine too')`, r.playlist, r.playlist2, r.user)
	h.exec(`INSERT INTO user_playlist_items (playlist_id, video_id, position) VALUES ($1, $3, 0), ($1, $4, 1), ($1, $5, 2), ($2, $4, 0)`,
		r.playlist, r.playlist2, pfKid, pfTeen, pfTeen2)
	made := h.raw(maker, http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"library","id":"`+pfLibA+`"},"shuffle":true}`)
	var q struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(made.Body.Bytes(), &q)
	if made.Code != http.StatusCreated || q.ID == "" || !strings.Contains(made.Body.String(), pfTeen) {
		r.leak("prepare", "the queue made before the limit lacks the 16+ video: %d %s", made.Code, made.Body.String())
	}
	r.queue = q.ID
	opened := h.raw(maker, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfTeen+`"}`)
	var st struct {
		ID string `json:"session_id"`
	}
	_ = json.Unmarshal(opened.Body.Bytes(), &st)
	if opened.Code/100 != 2 || st.ID == "" {
		r.leak("prepare", "no stream of the 16+ video opened before the limit: %d %s", opened.Code, opened.Body.String())
	}
	r.session = st.ID
	watching := h.raw(maker, http.MethodPost, "/api/watch/start", `{"video_id":"`+pfTeen+`"}`)
	_ = json.Unmarshal(watching.Body.Bytes(), &st)
	if watching.Code/100 != 2 || st.ID == "" || st.ID == r.session {
		r.leak("prepare", "no watch of the 16+ video started before the limit: %d %s", watching.Code, watching.Body.String())
	}
	r.watch = st.ID
	// the caller's pins, set while every library was its to pin
	h.exec(`DELETE FROM parental_rules WHERE user_id = $1 AND subject_kind = 'library'`, r.user)
	h.cache.Invalidate("")
	if pinned := h.raw(maker, http.MethodPut, "/api/me/pinned-libraries", `{"library_ids":["`+pfLibA+`","`+pfLibB+`"]}`); pinned.Code/100 != 2 {
		r.leak("prepare", "the libraries were not pinned: %d %s", pinned.Code, pinned.Body.String())
	}
	if child && len(r.fx.hidden[parental.SubjectLibrary]) > 0 {
		h.exec(`INSERT INTO parental_rules (user_id, effect, subject_kind, subject_id) VALUES ($1, 'block', 'library', $2)`, pfChild, pfLibB)
	}
	if child {
		h.exec(`UPDATE parental_policies SET max_tier = 9, version = version + 1 WHERE user_id = $1`, pfChild)
		h.cache.Invalidate("")
	}
}

// pbOther fills a path's other parameters (the ones that are not the object the gate
// checks) with a fixture the caller may see.
var pbOther = map[string]string{"video_id": pfKid}

// pbCustom are the content routes a path built from the registry cannot probe: their
// fixture is in the body, is the caller's own, or sits under another object.
var pbCustom map[string]func(r *pbRun, key string)

func init() {
	ids := func(id string) string { return `{"video_ids":["` + id + `"]}` }
	pbCustom = map[string]func(r *pbRun, key string){
		// the pipeline's collections: inside a library the caller may open
		"GET /api/libraries/{id}/collections/{kind}/{gid}": func(r *pbRun, key string) {
			base := "/api/libraries/" + pfLibA + "/collections/"
			r.ask(key, http.MethodGet, base+"topic/"+pfTopic, "")
			r.ask(key, http.MethodGet, base+"clip/"+pfClip, "")
			r.alike(key, http.MethodGet, base+"topic/{id}", "", pfTopic16)
			r.alike(key, http.MethodGet, base+"clip/{id}", "", pfClip16)
			for _, lib := range r.fx.hidden[parental.SubjectLibrary] {
				r.alike(key, http.MethodGet, "/api/libraries/{id}/collections/topic/"+pfTopic, "", lib)
			}
		},
		// the caller's playlists, which hold what is hidden now
		"GET /api/me/playlists/{id}": func(r *pbRun, key string) {
			r.ask(key, http.MethodGet, "/api/me/playlists/"+r.playlist, "")
		},
		"GET /api/me/playlists/{id}/items": func(r *pbRun, key string) {
			r.ask(key, http.MethodGet, "/api/me/playlists/"+r.playlist+"/items", "")
		},
		"POST /api/me/playlists": func(r *pbRun, key string) {
			for _, id := range r.fx.hidden[parental.SubjectVideo] {
				r.alike(key, http.MethodPost, "/api/me/playlists", `{"name":"pb","video_ids":["{id}"]}`, id)
			}
		},
		"PATCH /api/me/playlists/{id}": func(r *pbRun, key string) {
			r.ask(key, http.MethodPatch, "/api/me/playlists/"+r.playlist, `{"description":"pb"}`)
			for _, id := range r.fx.hidden[parental.SubjectVideo] {
				r.alike(key, http.MethodPatch, "/api/me/playlists/"+r.playlist, `{"video_ids":["{id}"]}`, id)
			}
		},
		"POST /api/me/playlists/{id}/items": func(r *pbRun, key string) {
			for _, id := range r.fx.hidden[parental.SubjectVideo] {
				r.alike(key, http.MethodPost, "/api/me/playlists/"+r.playlist+"/items", ids("{id}"), id)
			}
		},
		"PUT /api/me/playlists/{id}/order": func(r *pbRun, key string) {
			// the playlist as it was made, and its whole order as an account that sees
			// all of it would send it
			r.h.exec(`DELETE FROM user_playlist_items WHERE playlist_id = $1`, r.playlist)
			r.h.exec(`INSERT INTO user_playlist_items (playlist_id, video_id, position) VALUES ($1, $2, 0), ($1, $3, 1), ($1, $4, 2)`,
				r.playlist, pfKid, pfTeen, pfTeen2)
			r.alike(key, http.MethodPut, "/api/me/playlists/"+r.playlist+"/order", `{"video_ids":["`+pfTeen2+`","{id}","`+pfKid+`"]}`, pfTeen)
		},
		"DELETE /api/me/playlists/{id}/items/{video_id}": func(r *pbRun, key string) {
			// the hidden video is one of the playlist's items
			r.alike(key, http.MethodDelete, "/api/me/playlists/"+r.playlist+"/items/{id}", "", pfTeen2)
		},
		"DELETE /api/me/playlists/{id}": func(r *pbRun, key string) {
			r.ask(key, http.MethodDelete, "/api/me/playlists/"+r.playlist2, "")
		},
		// the caller's play queue, made before the limit
		"GET /api/me/play-queues/{id}": func(r *pbRun, key string) {
			r.ask(key, http.MethodGet, "/api/me/play-queues/"+r.queue, "")
			r.ask(key, http.MethodGet, "/api/me/play-queues/"+r.queue+"?around=0&before=0&after=50", "")
		},
		"POST /api/me/play-queues": func(r *pbRun, key string) {
			r.ask(key, http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"library","id":"`+pfLibA+`"}}`)
			r.ask(key, http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"collection","id":"topic:`+pfTopic+`"}}`)
			r.ask(key, http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"playlist","id":"`+r.playlist+`"}}`)
			for kind, list := range map[string][]string{"video": r.fx.hidden[parental.SubjectVideo], "series": r.fx.hidden[parental.SubjectSeries],
				"collection": r.fx.hidden[parental.SubjectCollection], "library": r.fx.hidden[parental.SubjectLibrary]} {
				for _, id := range list {
					r.alike(key, http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"`+kind+`","id":"{id}"}}`, id)
				}
			}
			r.alike(key, http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"collection","id":"topic:{id}"}}`, pfTopic16)
			r.alike(key, http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"collection","id":"clip:{id}"}}`, pfClip16)
		},
		"POST /api/me/play-queues/{id}/items": func(r *pbRun, key string) {
			for _, id := range r.fx.hidden[parental.SubjectVideo] {
				r.alike(key, http.MethodPost, "/api/me/play-queues/"+r.queue+"/items", `{"video_ids":["{id}"],"where":"next"}`, id)
			}
		},
		"POST /api/me/play-queues/{id}/reshuffle": func(r *pbRun, key string) {
			r.ask(key, http.MethodPost, "/api/me/play-queues/"+r.queue+"/reshuffle", "{}")
		},
		"PUT /api/me/play-queues/{id}/current": func(r *pbRun, key string) {
			for _, v := range []string{"1", "2", "3"} { // whatever version the queue is at now
				r.ask(key, http.MethodPut, "/api/me/play-queues/"+r.queue+"/current", `{"pos":0,"version":`+v+`}`)
			}
		},
		"DELETE /api/me/play-queues/{id}": func(r *pbRun, key string) {
			r.ask(key, http.MethodDelete, "/api/me/play-queues/"+r.queue, "")
		},
		// marks and pins: the fixture is in the body
		"POST /api/me/favorites": func(r *pbRun, key string) {
			for _, id := range r.fx.hidden[parental.SubjectVideo] {
				r.alike(key, http.MethodPost, "/api/me/favorites", `{"video_ids":["{id}"],"favorite":true}`, id)
			}
		},
		"POST /api/me/watched": func(r *pbRun, key string) {
			for _, id := range r.fx.hidden[parental.SubjectVideo] {
				r.alike(key, http.MethodPost, "/api/me/watched", `{"video_ids":["{id}"],"watched":true}`, id)
			}
		},
		"PUT /api/me/pinned-libraries": func(r *pbRun, key string) {
			r.ask(key, http.MethodPut, "/api/me/pinned-libraries", `{"library_ids":["`+pfLibA+`"]}`)
			for _, id := range r.fx.hidden[parental.SubjectLibrary] {
				r.alike(key, http.MethodPut, "/api/me/pinned-libraries", `{"library_ids":["{id}"]}`, id)
			}
		},
		// A3: search by title and by transcript, its pages and one hidden video's passages
		"POST /api/search": func(r *pbRun, key string) {
			rec := r.ask(key, http.MethodPost, "/api/search", `{"q":"zarqa"}`)
			var found struct {
				SearchID string `json:"search_id"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &found)
			if rec.Code != http.StatusOK || found.SearchID == "" {
				r.leak(key, "the search did not run: %d %s", rec.Code, rec.Body.String())
			}
			r.searchID = found.SearchID
		},
		"GET /api/search/results/{sid}": func(r *pbRun, key string) {
			r.search()
			for _, list := range []string{"titles", "transcripts"} {
				r.ask(key, http.MethodGet, "/api/search/results/"+r.searchID+"?list="+list, "")
			}
		},
		"GET /api/search/passages": func(r *pbRun, key string) {
			r.search()
			for _, id := range r.fx.hidden[parental.SubjectVideo] {
				r.alike(key, http.MethodGet, "/api/search/passages?sid="+r.searchID+"&video_id={id}", "", id)
			}
		},
		"GET /api/search/suggest": func(r *pbRun, key string) {
			r.ask(key, http.MethodGet, "/api/search/suggest?q=zar", "")
		},
		// A9: a stream, a watch for a hidden video; a stream opened before the limit
		"POST /api/stream/sessions": func(r *pbRun, key string) {
			for _, id := range r.fx.hidden[parental.SubjectVideo] {
				r.alike(key, http.MethodPost, "/api/stream/sessions", `{"video_id":"{id}"}`, id)
			}
		},
		"GET /api/stream/sessions/{id}": func(r *pbRun, key string) {
			r.alike(key, http.MethodGet, "/api/stream/sessions/{id}", "", r.session)
		},
		"POST /api/watch/start": func(r *pbRun, key string) {
			for _, id := range r.fx.hidden[parental.SubjectVideo] {
				r.alike(key, http.MethodPost, "/api/watch/start", `{"video_id":"{id}"}`, id)
			}
		},
		"POST /api/watch/heartbeat": func(r *pbRun, key string) {
			// a watch of the 16+ video started before the limit
			r.alike(key, http.MethodPost, "/api/watch/heartbeat", `{"session_id":"{id}","position_sec":5}`, r.watch)
		},
		// A2: a thumbnail by its content hash (the hash is no secret, the picture is)
		"GET /api/thumbnails/{hash}/small.jpg": func(r *pbRun, key string) {
			for _, hash := range r.h.hashes(r.fx.hidden[parental.SubjectVideo]...) {
				a := r.do(http.MethodGet, "/api/thumbnails/"+hash+"/small.jpg", "")
				b := r.do(http.MethodGet, "/api/thumbnails/"+strings.Repeat("0", len(hash))+"/small.jpg", "")
				if a.Code != b.Code || a.Body.Len() != b.Body.Len() {
					r.leak(key, "a hidden video's hash answers %d (%d bytes), an unknown hash %d (%d bytes)", a.Code, a.Body.Len(), b.Code, b.Body.Len())
				}
			}
		},
		// A10, A11: a hidden channel's signed URL and whether it may be watched
		"POST /api/channels/{id}/play": func(r *pbRun, key string) {
			for _, id := range r.fx.hidden[parental.SubjectChannel] {
				r.alike(key, http.MethodPost, "/api/channels/{id}/play", "{}", id)
			}
		},
	}
}

// search makes the caller's kept search once.
func (r *pbRun) search() {
	if r.searchID != "" {
		return
	}
	cur := r.cur
	r.cur = ""
	rec := r.do(http.MethodPost, "/api/search", `{"q":"zarqa"}`)
	r.cur = cur
	var found struct {
		SearchID string `json:"search_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &found)
	r.searchID = found.SearchID
	if r.searchID == "" {
		r.searchID = uuid.NewString()
	}
}

// registry probes every content route of the home. A route with no probe that reaches it
// with a real fixture is itself a failure: a parameter no fixture fills, a write with
// nothing to send, a registry entry the home does not mount and no one excused.
func (r *pbRun) registry() {
	h := r.h
	routes := h.contentRoutes()
	mounted := map[string]bool{}
	for _, rt := range routes {
		mounted[rt.pattern] = true
		key := rt.key()
		r.cur = key
		switch custom := pbCustom[key]; {
		case custom != nil:
			custom(r, key)
		case strings.HasPrefix(rt.pattern, "/ws/"):
			r.feed(rt, key)
		case rt.s.Class == parental.ByID:
			r.byID(rt, key)
		case rt.method == http.MethodGet && rt.s.Probe != "" && pbPlaceholder.MatchString(rt.s.Probe):
			// a filtered route about one object of a kind (a channel)
			m := pbPlaceholder.FindStringSubmatch(rt.s.Probe)
			path := strings.Replace(rt.s.Probe, m[0], "{id}", 1)
			if left := pbParam.FindAllString(strings.Replace(path, "{id}", "", 1), -1); len(left) > 0 {
				r.cur = ""
				r.leak(key, "the rehearsal has no fixture for %v: the route was not probed", left)
				continue
			}
			for _, id := range r.fx.hidden[m[1]] {
				r.alike(key, http.MethodGet, path, "", id)
			}
			for _, id := range r.fx.shown[m[1]] {
				r.ask(key, http.MethodGet, strings.Replace(path, "{id}", id, 1), "")
			}
		case rt.method == http.MethodGet && !strings.Contains(rt.pattern, "{"):
			r.ask(key, http.MethodGet, rt.pattern, "")
		}
		r.cur = ""
		if !r.covered[key] {
			r.leak(key, "the rehearsal has no probe that reaches this route with a fixture: it was not probed")
		}
	}
	for _, s := range parental.Surfaces {
		if (s.Class != parental.Filtered && s.Class != parental.ByID) || mounted[s.Pattern] {
			if _, excused := pbNotMounted[s.Pattern]; excused && mounted[s.Pattern] {
				r.leak(s.Pattern, "the home mounts this route: it may not be excused from its probe (pbNotMounted)")
			}
			continue
		}
		if pbNotMounted[s.Pattern] == "" {
			r.leak(s.Pattern, "the rehearsal's home does not mount this content route and nothing says where it is tested: it was not probed")
		}
	}
}

// pbBodies are the bodies of the writes whose handler reads its body before anything else.
var pbBodies = map[string]string{
	"POST /api/videos/{id}/enrichment/accept": `{"external_id":"pb"}`,
}

// pbBody is the body a generic probe sends a route.
func pbBody(rt pbRoute) string {
	if b, ok := pbBodies[rt.key()]; ok {
		return b
	}
	if rt.method != http.MethodGet && rt.method != http.MethodDelete {
		return "{}"
	}
	return ""
}

// byID probes a route that names one object in its path: each hidden fixture of its kind
// answers as an id that names nothing, and a read of each one the caller may see names no
// hidden subject in what it lists.
func (r *pbRun) byID(rt pbRoute, key string) {
	path := rt.pattern
	var unfilled []string
	path = pbParam.ReplaceAllStringFunc(path, func(seg string) string {
		name := pbParam.FindStringSubmatch(seg)[1]
		if name == rt.s.Param {
			return "{id}"
		}
		if v, ok := pbOther[name]; ok {
			return v
		}
		unfilled = append(unfilled, seg)
		return seg
	})
	if len(unfilled) > 0 || !strings.Contains(path, "{id}") {
		r.cur = ""
		r.leak(key, "the rehearsal has no fixture for %v: the route was not probed", unfilled)
		return
	}
	body := pbBody(rt)
	for _, id := range r.fx.hidden[rt.s.Kind] {
		r.alike(key, rt.method, path, body, id)
	}
	if rt.method != http.MethodGet {
		return // a write is sent for hidden ids only: it must change nothing
	}
	for _, id := range r.fx.shown[rt.s.Kind] {
		r.ask(key, http.MethodGet, strings.Replace(path, "{id}", id, 1), "")
	}
}

// feed is a live feed's probe: events naming hidden subjects are published, then one
// every account is sent, and what arrived is scanned.
func (r *pbRun) feed(rt pbRoute, key string) {
	h := r.h
	var path, channel string
	switch rt.pattern {
	case "/ws/home":
		path, channel = "/ws/home", ws.HomeChannel
	case "/ws/library/{id}":
		for _, id := range r.fx.hidden[parental.SubjectLibrary] {
			r.alike(key, http.MethodGet, "/ws/library/{id}", "", id)
		}
		path, channel = "/ws/library/"+pfLibA, "library:"+pfLibA
	default:
		// a feed about one video: a hidden one answers as one that is not there
		for _, id := range r.fx.hidden[parental.SubjectVideo] {
			r.alike(key, http.MethodGet, strings.Replace(rt.pattern, "{"+rt.s.Param+"}", "{id}", 1), "", id)
		}
		return
	}
	r.asked++
	r.covered[key] = true
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
		r.leak(key, "the feed never got the closing event: %s", f.body())
	}
	r.scan(key, f.close())
}

// attempts are the surfaces that are not content routes of the registry: GraphQL (which
// answers through sub-requests) and what a limited caller may not do at all.
func (r *pbRun) attempts() {
	fx := r.fx
	// A7: GraphQL
	for _, id := range fx.hidden[parental.SubjectVideo] {
		r.alike("POST /graphql video", http.MethodPost, "/graphql", `{"query":"{ video(id: \"{id}\") { id title } }"}`, id)
	}
	r.ask("POST /graphql videos", http.MethodPost, "/graphql", `{"query":"{ videos(limit: 50) { items { id title } } }"}`)
	r.ask("POST /graphql collections", http.MethodPost, "/graphql", `{"query":"{ collections { id name } }"}`)
	for _, id := range fx.hidden[parental.SubjectCollection] {
		r.alike("POST /graphql collection", http.MethodPost, "/graphql", `{"query":"{ collection(id: \"{id}\") { id name } }"}`, id)
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

// own are the attempts only the restricted account itself can make: A15 (changing its own
// policy, naming itself a guardian, answering a request). A5, the play queue and the stream
// made before the limit was tightened, is prepare's, and the registry's probes read them.
func (r *pbRun) own() {
	h := r.h
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
	r.prepare()
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

// T04: no caller held to 9+ is shown anything above it, on any surface, and every content
// route of the home was probed: a route the rehearsal could not reach with a fixture is a
// failure of its own (registry).
func TestParentalBypass(t *testing.T) {
	h := pbHome(t)
	want := len(h.contentRoutes())
	if want < 100 {
		t.Fatalf("the home has only %d content routes", want)
	}
	for _, c := range pbCallers {
		r := rehearse(h, c.who, h.fixtures(c.rules), c.own)
		if len(r.covered) != want {
			t.Errorf("%s: %d of the home's %d content routes were probed", c.who, len(r.covered), want)
		}
		for _, l := range r.leaks {
			t.Errorf("%s: %s", c.who, l)
		}
		t.Logf("%s: %d content routes probed, %d requests, %d leaks", c.who, len(r.covered), r.asked, len(r.leaks))
	}
}

// pbBlind are the content routes whose answer shows an account nothing limits no
// difference between a hidden fixture and an id that names nothing, each with why. The
// rehearsal still probes each of them with a hidden fixture (a restricted account must be
// answered the same for both), but its own proof below cannot use them. A route may be
// listed only while that is true: one listed here that does show the difference fails, so
// the list cannot hide a route. A route that refuses a viewer by its role before it reads
// anything needs no entry: the proof finds that out itself.
var pbBlind = map[string]string{
	"GET /api/search/recent":                       "the caller's own search words with counts; a count is a number, not a marker (TestP57Gaps checks the numbers)",
	"GET /api/search/suggest":                      "only the caller's own earlier search words are read: there is nothing of a video in the answer",
	"GET /api/thumbnails/{hash}/small.jpg":         "the home keeps no thumbnail files, so every hash answers 404; handlers/reports tests the picture with files",
	"GET /api/videos/{video_id}/downloaded":        "keyed by a registered device's header, which the rehearsal has none of: every id answers the same refusal",
	"POST /api/videos/{video_id}/downloaded":       "as GET /api/videos/{video_id}/downloaded",
	"PATCH /api/videos/{video_id}/downloaded":      "as GET /api/videos/{video_id}/downloaded",
	"DELETE /api/videos/{video_id}/downloaded":     "as GET /api/videos/{video_id}/downloaded",
	"DELETE /api/me/history/{video_id}":            "answers 204 with no body for any id",
	"DELETE /api/recommendations/items/{video_id}": "answers the same with no content for any id",
	"DELETE /api/me/play-queues/{id}":              "removes the caller's own queue; the answer has no body",
	"DELETE /api/me/playlists/{id}":                "removes the caller's own playlist; the answer has no body",
}

// The rehearsal can see: the same questions asked by an account nothing limits find the
// hidden subjects on every content route and on every other surface. The exceptions are
// the routes pbBlind explains and the ones that refuse a viewer by its role before reading
// anything (an admin's or a reviewer's: 401 or 403 for an object the viewer may see).
// It is also story 57.12's rule that nothing becomes restricted by itself: a viewer with
// no policy is shown all of it.
func TestParentalBypassSeesWhatIsThere(t *testing.T) {
	h := pbHome(t)
	// library B and what is in it count as hidden subjects too (they are, for the child)
	fx := h.fixtures(true)
	r := &pbRun{h: h, who: "viewer", fx: fx}
	r.prepare()
	r.registry()
	r.attempts()
	seen := map[string]bool{}
	for _, l := range r.leaks {
		surface, _, _ := strings.Cut(l, ": ")
		seen[surface] = true
		if strings.Contains(l, "was not probed") || strings.Contains(l, "did not run") || surface == "prepare" {
			t.Errorf("viewer: %s", l)
		}
	}
	// byRole reports whether the route refuses the viewer for an object it may see
	byRole := func(rt pbRoute) bool {
		path := rt.pattern
		if rt.s.Class == parental.ByID {
			shown := fx.shown[rt.s.Kind]
			if len(shown) == 0 {
				return false
			}
			path = pbParam.ReplaceAllStringFunc(rt.pattern, func(seg string) string {
				if pbParam.FindStringSubmatch(seg)[1] == rt.s.Param {
					return shown[0]
				}
				return pfKid
			})
		} else if strings.Contains(path, "{") {
			return false
		}
		code := h.raw("viewer", rt.method, path, pbBody(rt)).Code
		return code == http.StatusUnauthorized || code == http.StatusForbidden
	}
	refused := 0
	for _, rt := range h.contentRoutes() {
		key := rt.key()
		why, blind := pbBlind[key]
		switch {
		case seen[key] && blind:
			t.Errorf("%s is in pbBlind (%s) but shows an unrestricted viewer the hidden fixtures: take it off the list", key, why)
		case seen[key] || blind:
		case byRole(rt):
			refused++
		default:
			t.Errorf("%s shows an unrestricted viewer nothing of the hidden fixtures: the rehearsal could not tell a leak there (give it a fixture, or say why in pbBlind)", key)
		}
	}
	for key := range pbBlind {
		found := false
		for _, rt := range h.contentRoutes() {
			found = found || rt.key() == key
		}
		if !found {
			t.Errorf("pbBlind lists %s, which is not a content route of the home", key)
		}
	}
	for _, surface := range []string{
		"POST /graphql video", "POST /graphql videos", "POST /graphql collection", "POST /api/me/tokens", "GET /api/channels/xmltv",
	} {
		if !seen[surface] {
			t.Errorf("%s shows an unrestricted viewer nothing of the hidden fixtures: the rehearsal could not tell a leak there", surface)
		}
	}
	if len(seen) < 70 {
		t.Errorf("only %d surfaces show an unrestricted viewer the hidden fixtures", len(seen))
	}
	t.Logf("an unrestricted viewer: %d surfaces show the fixtures, %d content routes refuse a viewer by role, %d are blind", len(seen), refused, len(pbBlind))
}

// T05: with the filter taken off one surface the rehearsal fails, naming it. The filter
// is taken off by answering that surface as the unrestricted viewer would be answered.
func TestParentalBypassCatchesAMissingFilter(t *testing.T) {
	h := pbHome(t)
	viewer := &principal.Principal{UserID: pfViewer, Libraries: []string{pfLibA, pfLibB}, Source: principal.SourceJWT}
	for _, c := range []struct{ name, method, prefix, surface string }{
		{"a list", http.MethodGet, "/api/videos", "GET /api/videos"},
		{"an object by id", http.MethodGet, "/api/series/", "GET /api/series/{id}/episodes"},
		{"a pipeline collection under a library", http.MethodGet, "/api/libraries/" + pfLibA + "/collections/", "GET /api/libraries/{id}/collections/{kind}/{gid}"},
		{"search", http.MethodPost, "/api/search", "POST /api/search"},
		{"GraphQL", http.MethodPost, "/graphql", "POST /graphql videos"},
		{"opening a stream", http.MethodPost, "/api/stream/sessions", "POST /api/stream/sessions"},
		{"a live feed", http.MethodGet, "/ws/home", "GET /ws/home"},
		{"live TV", http.MethodGet, "/api/channels", "GET /api/channels"},
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

// The rehearsal fails on a content route it cannot reach: one whose path has a parameter no
// fixture fills, one with no probe at all, and a registry entry the home does not mount
// that no one excused. (This is the class that hid an unfiltered route: its probe was sent
// with placeholders for the collection's kind and id, the route answered "not found", and
// that counted as asked.)
func TestParentalBypassFailsOnARouteItCannotProbe(t *testing.T) {
	h := pbHome(t)
	names := func(r *pbRun, surface, what string) bool {
		for _, l := range r.leaks {
			if strings.HasPrefix(l, surface+": ") && strings.Contains(l, what) {
				return true
			}
		}
		return false
	}
	// its own probe taken away, the pipeline collections' route has parameters nothing fills
	const groups = "GET /api/libraries/{id}/collections/{kind}/{gid}"
	custom := pbCustom[groups]
	delete(pbCustom, groups)
	r := rehearse(h, "child", h.fixtures(true), false)
	pbCustom[groups] = custom
	if !names(r, groups, "no fixture for [{kind} {gid}]") || r.covered[groups] {
		t.Errorf("with no fixture for {kind} and {gid} the rehearsal does not fail on %s: %v", groups, r.leaks)
	}
	// a write whose fixture is in its body, with no probe
	const marks = "POST /api/me/favorites"
	custom = pbCustom[marks]
	delete(pbCustom, marks)
	r = rehearse(h, "child", h.fixtures(true), false)
	pbCustom[marks] = custom
	if !names(r, marks, "was not probed") || r.covered[marks] {
		t.Errorf("with no probe the rehearsal does not fail on %s: %v", marks, r.leaks)
	}
	// a content entry of the registry the home does not mount, excused by no one
	why := pbNotMounted["/api/panels"]
	delete(pbNotMounted, "/api/panels")
	r = rehearse(h, "child", h.fixtures(true), false)
	pbNotMounted["/api/panels"] = why
	if !names(r, "/api/panels", "was not probed") {
		t.Errorf("an unmounted content route nobody excused does not fail the rehearsal: %v", r.leaks)
	}
	// and a mounted route may not be excused
	pbNotMounted["/api/videos"] = "no reason"
	r = rehearse(h, "child", h.fixtures(true), false)
	delete(pbNotMounted, "/api/videos")
	if !names(r, "/api/videos", "may not be excused") {
		t.Errorf("a mounted route excused from its probe does not fail the rehearsal: %v", r.leaks)
	}
}
