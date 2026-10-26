package analytics

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// The seeded home, the same on SQLite and Postgres. Now is Monday
// 2026-10-26 15:00 UTC; the default range (7d) starts on the 19th.
//
//	plays     P1 sara  Omar E1 (Series) 13:00, 1800 s, completed, player/ios/phone
//	          P2 omar  Talk (Talks)     yesterday 15:00, 600 s, web/macos/desktop
//	          P3 sara  Omar E1          14:50, 300 s, playing now (no app: unknown)
//	          P4 omar  Omar E1          31 days ago (outside 7d)
//	viewings  T1 sara  Quran  app   12:00-13:00, 3600 s, tv/tvos/tv
//	          T2 -     Quran  tuner 12:30-12:50, 1200 s
//	          T3 omar  News   app   14:55, open and fresh, 300 s, web
//	          T4 -     News   tuner 14:00, open but quiet since 14:50, 3000 s
//	          T5 sara  Quran  marked remote in tv_watch_sessions: ignored
//	remote    R1 lina  Quran  14:00, heartbeat 14:59, open, 3540 s
const (
	uSara  = "11111111-1111-4111-8111-111111111111"
	uOmar  = "22222222-2222-4222-8222-222222222222"
	uLina  = "33333333-3333-4333-8333-333333333333"
	lSer   = "44444444-4444-4444-8444-444444444441"
	lTalk  = "44444444-4444-4444-8444-444444444442"
	vOmar  = "55555555-5555-4555-8555-555555555551"
	vTalk  = "55555555-5555-4555-8555-555555555552"
	cQuran = "66666666-6666-4666-8666-666666666661"
	cNews  = "66666666-6666-4666-8666-666666666662"
	tDrama = "77777777-7777-4777-8777-777777777771"
)

var fixtureNow = time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)

// seed fills the home. at binds a time the way the dialect's writers do
// (SQLite: text, in more than one format).
func seed(t *testing.T, db *sql.DB, at func(time.Time, int) any) {
	t.Helper()
	now := fixtureNow
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for i, u := range [][2]string{{uSara, "sara"}, {uOmar, "omar"}, {uLina, "lina"}} {
		exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, $2, 'x', $3)`, u[0], u[1], i == 0)
	}
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'Series', '{}'), ($2, 'Talks', '{}')`, lSer, lTalk)
	for i, v := range [][3]string{{vOmar, lSer, "Omar E1"}, {vTalk, lTalk, "Talk"}} {
		hash := strings.Repeat(strconv.Itoa(i+1), 64) // 64 lower hex, as slot 0003 checks
		exec(`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, title)
			VALUES ($1, $2, $3, $4, $5, 1, $6, $7)`, v[0], v[1], hash, "/m/"+v[2], v[2]+".mp4", at(now, 0), v[2])
	}
	exec(`INSERT INTO tags (id, name, name_norm) VALUES ($1, 'Drama', 'drama')`, tDrama)
	exec(`INSERT INTO video_tags (video_id, tag_id) VALUES ($1, $2)`, vOmar, tDrama)
	exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 1, 'Quran', 'quran', 'shuffle'),
		($2, 2, 'News', 'news', 'shuffle')`, cQuran, cNews)

	play := func(id, user, video string, start time.Time, sec int, state string, hb time.Time, ended *time.Time,
		pct float64, app, platform, device any, f int) {
		var end any
		if ended != nil {
			end = at(*ended, f)
		}
		exec(`INSERT INTO watch_sessions (id, user_id, video_id, started_at, ended_at, last_heartbeat,
			duration_sec, percent_complete, state, client_app, client_version, platform, device_type)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			id, user, video, at(start, f), end, at(hb, f), sec, pct, state, app, nil, platform, device)
	}
	e1, e2, e4 := ago(90*time.Minute), ago(23*time.Hour), ago(31*24*time.Hour)
	play("a0000000-0000-4000-8000-000000000001", uSara, vOmar, ago(2*time.Hour), 1800, "completed", e1, &e1, 100, "player", "ios", "phone", 0)
	play("a0000000-0000-4000-8000-000000000002", uOmar, vTalk, ago(24*time.Hour), 600, "stopped", e2, &e2, 40, "web", "macos", "desktop", 1)
	play("a0000000-0000-4000-8000-000000000003", uSara, vOmar, ago(10*time.Minute), 300, "active", ago(30*time.Second), nil, 20, nil, nil, nil, 2)
	play("a0000000-0000-4000-8000-000000000004", uOmar, vOmar, ago(31*24*time.Hour+time.Hour), 999, "stopped", e4, &e4, 90, "web", "web", "desktop", 0)

	view := func(id, channel string, user any, surface string, start, seen time.Time, ended bool, sec int,
		app, platform, device any, f int) {
		var end, reason any
		if ended {
			end, reason = at(seen, f), "closed"
		}
		exec(`INSERT INTO tv_watch_sessions (id, channel_id, user_id, surface, client_app, client_version,
			platform, device_type, started_at, last_seen_at, ended_at, seconds, end_reason)
			VALUES ($1, $2, $3, $4, $5, NULL, $6, $7, $8, $9, $10, $11, $12)`,
			id, channel, user, surface, app, platform, device, at(start, f), at(seen, f), end, sec, reason)
	}
	view("b0000000-0000-4000-8000-000000000001", cQuran, uSara, "app", ago(3*time.Hour), ago(2*time.Hour), true, 3600, "tv", "tvos", "tv", 0)
	view("b0000000-0000-4000-8000-000000000002", cQuran, nil, "tuner", ago(150*time.Minute), ago(130*time.Minute), true, 1200, nil, nil, nil, 1)
	view("b0000000-0000-4000-8000-000000000003", cNews, uOmar, "app", ago(5*time.Minute), ago(20*time.Second), false, 300, "web", "web", "desktop", 2)
	view("b0000000-0000-4000-8000-000000000004", cNews, nil, "tuner", ago(time.Hour), ago(10*time.Minute), false, 3000, nil, nil, nil, 0)
	view("b0000000-0000-4000-8000-000000000005", cQuran, uSara, "remote", ago(time.Hour), ago(time.Minute), false, 7777, nil, nil, nil, 1)

	exec(`INSERT INTO tv_remote_sessions (id, user_id, channel_id, device, started_at, last_beat_at)
		VALUES ($1, $2, $3, 'phone', $4, $5)`, "c0000000-0000-4000-8000-000000000001", uLina, cQuran,
		at(ago(time.Hour), 2), at(ago(time.Minute), 2))

	exec(`INSERT INTO tv_remote_usage_daily (day, minutes_delivered, cost_usd, simulcast_minutes) VALUES
		('2026-10-25', 120, 0.12, 10), ('2026-10-26', 60, 0.06, 0), ('2026-09-01', 999, 9, 0)`)
	exec(`INSERT INTO api_usage_daily (day, client_app, platform, requests, errors, total_ms, slow) VALUES
		('2026-10-26', 'player', 'ios', 100, 5, 10000, 2),
		('2026-10-25', 'player', 'android', 50, 0, 2500, 0),
		('2026-10-26', 'web', 'web', 200, 0, 4000, 1),
		('2026-08-01', 'web', 'web', 999, 99, 1, 1)`)
	exec(`INSERT INTO api_usage_people_daily (day, client_app, user_id, client_version) VALUES
		('2026-10-26', 'player', $1, '1.3.0'), ('2026-10-25', 'player', $2, '1.2.0'),
		('2026-10-26', 'web', $2, '0.3.25'), ('2026-10-26', 'remote', $3, '1.0'),
		('2026-08-01', 'tv', $1, '0.9')`, uSara, uOmar, uLina)
}

// get calls an endpoint as the admin and decodes its JSON.
func get(t *testing.T, r http.Handler, path string, out any) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: uSara, IsAdmin: true}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
	}
	if out == nil {
		return
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, rec.Body.String())
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// runScenario checks every endpoint against the seeded home.
func runScenario(t *testing.T, h *Handler) {
	h.NowFunc = func() time.Time { return fixtureNow }
	r := chi.NewRouter()
	h.Mount(r)
	const A = "/api/admin/analytics/"

	t.Run("summary", func(t *testing.T) {
		var s Summary
		get(t, r, A+"summary", &s)
		if s.Range != "7d" || s.WatchSec != 14340 || s.VideoSec != 2700 || s.TVSec != 11640 ||
			s.Plays != 3 || s.Viewings != 5 || s.People != 3 || !near(s.CompletionRate, 1.0/3) ||
			!near(s.LiveTVHours, 11640.0/3600) {
			t.Errorf("totals: %+v", s)
		}
		want := map[string][3]int64{"video": {3, 2700, 2}, "app": {2, 3900, 2}, "tuner": {2, 4200, 0}, "remote": {1, 3540, 1}}
		if len(s.Surfaces) != 4 || s.Surfaces[0].Label != "video" {
			t.Fatalf("surfaces: %+v", s.Surfaces)
		}
		for _, c := range s.Surfaces {
			if w := want[c.Label]; c.Sessions != w[0] || c.WatchSec != w[1] || c.People != w[2] {
				t.Errorf("surface %s: %+v want %v", c.Label, c, w)
			}
		}
		apps := map[string]int64{}
		for _, a := range s.Apps {
			apps[a.Label] = a.WatchSec
		}
		// a tuner viewing is Plex's, a remote TV one the web's; the play that named no
		// app is the one unknown
		if apps["unknown"] != 300 || apps["plex"] != 4200 || apps["tv"] != 3600 || apps["player"] != 1800 ||
			apps["web"] != 4440 || s.Apps[0].Label != "web" {
			t.Errorf("apps: %+v", s.Apps)
		}
		if len(s.Platforms) == 0 || len(s.Devices) == 0 {
			t.Errorf("platforms %v devices %v", s.Platforms, s.Devices)
		}

		// The filters.
		get(t, r, A+"summary?surface=tuner", &s)
		if s.Plays != 0 || s.Viewings != 2 || s.TVSec != 4200 || s.People != 0 {
			t.Errorf("tuner: %+v", s)
		}
		get(t, r, A+"summary?library="+lSer, &s)
		if s.Plays != 2 || s.Viewings != 0 || s.WatchSec != 2100 {
			t.Errorf("library: %+v", s)
		}
		get(t, r, A+"summary?user="+uOmar, &s)
		if s.Plays != 1 || s.Viewings != 1 || s.WatchSec != 900 || s.People != 1 {
			t.Errorf("user: %+v", s)
		}
		get(t, r, A+"summary?client_app=unknown", &s)
		if s.WatchSec != 300 || s.Plays != 1 || s.Viewings != 0 {
			t.Errorf("unknown app: %+v", s)
		}
		get(t, r, A+"summary?client_app=plex", &s)
		if s.WatchSec != 4200 || s.Viewings != 2 || s.Plays != 0 {
			t.Errorf("plex: %+v", s)
		}
		get(t, r, A+"summary?range=today&client_app=player", &s)
		if s.Range != "today" || s.WatchSec != 1800 || s.Plays != 1 {
			t.Errorf("today, player: %+v", s)
		}
		get(t, r, A+"summary?from=2026-10-25&to=2026-10-25", &s)
		if s.Range != "custom" || s.WatchSec != 600 || s.Plays != 1 || s.Viewings != 0 {
			t.Errorf("custom: %+v", s)
		}
		get(t, r, A+"summary?range=all&refresh=true", &s)
		if s.Plays != 4 || s.VideoSec != 3699 {
			t.Errorf("all: %+v", s)
		}
	})

	t.Run("now", func(t *testing.T) {
		var n NowResponse
		get(t, r, A+"now", &n)
		if len(n.Plays) != 1 || n.Plays[0].Title != "Omar E1" || n.Plays[0].Username != "sara" ||
			n.Plays[0].ClientApp != "unknown" || n.Plays[0].ElapsedSec != 600 {
			t.Errorf("plays: %+v", n.Plays)
		}
		if len(n.Viewings) != 2 || n.Counts["video"] != 1 || n.Counts["app"] != 1 || n.Counts["tuner"] != 0 || n.Counts["remote"] != 1 {
			t.Fatalf("viewings: %+v counts %v", n.Viewings, n.Counts)
		}
		byS := map[string]NowViewing{}
		for _, v := range n.Viewings {
			byS[v.Surface] = v
		}
		if v := byS["app"]; v.ChannelName != "News" || v.Username != "omar" || v.ClientApp != "web" || v.ElapsedSec != 300 {
			t.Errorf("app viewing: %+v", v)
		}
		if v := byS["remote"]; v.ChannelName != "Quran" || v.ChannelNumber != 1 || v.Username != "lina" || v.DeviceType != "phone" {
			t.Errorf("remote viewing: %+v", v)
		}
		get(t, r, A+"now?surface=video", &n)
		if len(n.Plays) != 1 || len(n.Viewings) != 0 {
			t.Errorf("video only: %+v", n)
		}
		get(t, r, A+"now?user="+uOmar, &n)
		if len(n.Plays) != 0 || len(n.Viewings) != 1 {
			t.Errorf("omar now: %+v", n)
		}
	})

	t.Run("activity", func(t *testing.T) {
		var a ActivityResponse
		get(t, r, A+"activity", &a)
		if a.Bucket != "day" || len(a.Series) != 2 {
			t.Fatalf("series: %+v", a.Series)
		}
		if p := a.Series[0]; p.Bucket != "2026-10-25" || p.VideoSec != 600 || p.Plays != 1 || p.TVSec != 0 {
			t.Errorf("day 1: %+v", p)
		}
		if p := a.Series[1]; p.Bucket != "2026-10-26" || p.VideoSec != 2100 || p.TVSec != 11640 ||
			p.WatchSec != 13740 || p.Viewings != 5 {
			t.Errorf("day 2: %+v", p)
		}
		dow := int(fixtureNow.Weekday())
		if a.Heatmap[dow][13] != 1800 || a.Heatmap[dow][12] != 4800 || a.Heatmap[dow][14] != 7140 {
			t.Errorf("heatmap row: %v", a.Heatmap[dow])
		}
		get(t, r, A+"activity?range=today", &a)
		if a.Bucket != "hour" || len(a.Series) != 3 || a.Series[0].Bucket != "2026-10-26T12:00" {
			t.Errorf("hours: %+v", a.Series)
		}
	})

	t.Run("top videos, libraries, genres", func(t *testing.T) {
		var tv struct{ Videos []TopVideo }
		get(t, r, A+"top-videos", &tv)
		if len(tv.Videos) != 2 || tv.Videos[0].Title != "Omar E1" || tv.Videos[0].Sessions != 2 ||
			tv.Videos[0].WatchSec != 2100 || tv.Videos[0].Viewers != 1 || !near(tv.Videos[0].CompletionRate, 0.5) {
			t.Errorf("top videos: %+v", tv.Videos)
		}
		var lb struct{ Libraries []LabelStat }
		get(t, r, A+"libraries", &lb)
		if len(lb.Libraries) != 2 || lb.Libraries[0].Label != "Series" || lb.Libraries[0].WatchSec != 2100 {
			t.Errorf("libraries: %+v", lb.Libraries)
		}
		var g struct{ Genres []CountStat }
		get(t, r, A+"genres", &g)
		if len(g.Genres) != 1 || g.Genres[0].Label != "Drama" || g.Genres[0].Sessions != 2 {
			t.Errorf("genres: %+v", g.Genres)
		}
		get(t, r, A+"top-videos?surface=app", &tv)
		if len(tv.Videos) != 0 {
			t.Errorf("no video is watched as a channel: %+v", tv.Videos)
		}
	})

	t.Run("top channels", func(t *testing.T) {
		var c TopChannelsResponse
		get(t, r, A+"top-channels", &c)
		if len(c.Channels) != 2 {
			t.Fatalf("channels: %+v", c.Channels)
		}
		q, n := c.Channels[0], c.Channels[1]
		if q.Name != "Quran" || q.Viewings != 3 || q.WatchSec != 8340 || q.People != 2 || q.PeakViewers != 2 || len(q.Surfaces) != 3 {
			t.Errorf("Quran: %+v", q)
		}
		if n.Name != "News" || n.Viewings != 2 || n.WatchSec != 3300 || n.People != 1 || n.PeakViewers != 1 {
			t.Errorf("News: %+v", n)
		}
		if c.PeakViewers != 2 || c.PeakAt != fixtureNow.Add(-150*time.Minute).Format(time.RFC3339) {
			t.Errorf("peak %d at %s", c.PeakViewers, c.PeakAt)
		}
		if len(c.Surfaces) != 3 || c.Surfaces[0].Label != "app" || c.Surfaces[2].WatchSec != 3540 {
			t.Errorf("surfaces: %+v", c.Surfaces)
		}
		get(t, r, A+"top-channels?surface=remote", &c)
		if len(c.Channels) != 1 || c.Channels[0].Viewings != 1 || c.PeakViewers != 1 {
			t.Errorf("remote only: %+v", c)
		}
	})

	t.Run("people", func(t *testing.T) {
		var p struct{ People []Person }
		get(t, r, A+"people", &p)
		if len(p.People) != 3 {
			t.Fatalf("people: %+v", p.People)
		}
		s, l, o := p.People[0], p.People[1], p.People[2]
		if s.Username != "sara" || s.WatchSec != 5700 || s.VideoSec != 2100 || s.TVSec != 3600 || s.Plays != 2 ||
			s.Viewings != 1 || s.Channels != 1 || strings.Join(s.Apps, ",") != "player,tv,unknown" ||
			s.LastActiveDay != "2026-10-26" || s.LastWatchedAt == "" {
			t.Errorf("sara: %+v", s)
		}
		if l.Username != "lina" || l.WatchSec != 3540 || strings.Join(l.Apps, ",") != "remote,web" {
			t.Errorf("lina: %+v", l)
		}
		if o.Username != "omar" || o.WatchSec != 900 || strings.Join(o.Apps, ",") != "player,web" {
			t.Errorf("omar: %+v", o)
		}
		get(t, r, A+"people?surface=video", &p)
		if len(p.People) != 2 {
			t.Errorf("video watchers: %+v", p.People)
		}
	})

	t.Run("apps", func(t *testing.T) {
		var a AppsResponse
		get(t, r, A+"apps", &a)
		if len(a.Apps) != 3 || a.Apps[0].ClientApp != "web" || a.Apps[1].ClientApp != "player" || a.Apps[2].ClientApp != "remote" {
			t.Fatalf("apps: %+v", a.Apps)
		}
		p := a.Apps[1]
		if p.Requests != 150 || p.Errors != 5 || !near(p.ErrorRate, 5.0/150) || p.Slow != 2 ||
			!near(p.AvgMs, 12500.0/150) || p.People != 2 || len(p.Versions) != 2 || len(p.Platforms) != 2 ||
			p.Platforms[0].Platform != "ios" || !near(p.Platforms[0].AvgMs, 100) {
			t.Errorf("player: %+v", p)
		}
		if a.Apps[2].Requests != 0 || a.Apps[2].People != 1 {
			t.Errorf("remote: %+v", a.Apps[2])
		}
		if len(a.Trend) != 2 || a.Trend[0].Day != "2026-10-25" || a.Trend[0].Requests != 50 || a.Trend[0].People != 1 ||
			a.Trend[1].Requests != 300 || a.Trend[1].Errors != 5 || a.Trend[1].People != 3 {
			t.Errorf("trend: %+v", a.Trend)
		}
		if len(a.Platforms) == 0 || len(a.Devices) == 0 {
			t.Errorf("platforms/devices: %+v %+v", a.Platforms, a.Devices)
		}
		get(t, r, A+"apps?client_app=player&user="+uSara, &a)
		if len(a.Apps) != 1 || a.Apps[0].Requests != 150 || a.Apps[0].People != 1 || a.Apps[0].Versions[0].Version != "1.3.0" {
			t.Errorf("player for sara: %+v", a.Apps)
		}
		get(t, r, A+"apps?range=all", &a)
		if len(a.Apps) != 4 {
			t.Errorf("all time: %+v", a.Apps)
		}
	})

	t.Run("remote tv", func(t *testing.T) {
		var rt RemoteTVResponse
		get(t, r, A+"remote-tv", &rt)
		if len(rt.Days) != 2 || !near(rt.MinutesDelivered, 180) || !near(rt.CostUSD, 0.18) || !near(rt.SimulcastMinutes, 10) ||
			rt.Viewings != 1 || rt.WatchSec != 3540 || rt.People != 1 {
			t.Errorf("remote: %+v", rt)
		}
		get(t, r, A+"remote-tv?surface=app", &rt)
		if rt.Viewings != 0 || len(rt.Days) != 2 {
			t.Errorf("remote under the app surface: %+v", rt)
		}
	})

	t.Run("export", func(t *testing.T) {
		req := httptest.NewRequest("GET", A+"export?format=csv", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: uSara, IsAdmin: true}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		recs, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
		if err != nil || rec.Code != 200 {
			t.Fatalf("csv: %d %v", rec.Code, err)
		}
		if len(recs) != 9 || strings.Join(recs[0], ",") != strings.Join(exportHeader, ",") {
			t.Fatalf("csv rows: %d %v", len(recs), recs[0])
		}
		kinds := map[string]int{}
		for _, row := range recs[1:] {
			kinds[row[0]+"/"+row[5]]++
		}
		if kinds["play/video"] != 3 || kinds["viewing/app"] != 2 || kinds["viewing/tuner"] != 2 || kinds["viewing/remote"] != 1 {
			t.Errorf("kinds: %v", kinds)
		}

		var rows []map[string]string
		get(t, r, A+"export?format=json&surface=remote", &rows)
		if len(rows) != 1 || rows[0]["kind"] != "viewing" || rows[0]["state"] != "open" || rows[0]["user_id"] != uLina {
			t.Errorf("json: %+v", rows)
		}
	})
}
