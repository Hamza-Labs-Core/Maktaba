package queues

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

const (
	seasonA = "11111111-1111-4111-8111-111111111111"
	seasonB = "22222222-2222-4222-8222-222222222222"
	seriesX = "33333333-3333-4333-8333-333333333333"
	videoV  = "44444444-4444-4444-8444-444444444444"
	libL    = "55555555-5555-4555-8555-555555555555"
)

// ---------- explain ----------

var explainVideoCols = []string{"id", "title", "filename", "library_id", "library", "aired", "date_quality",
	"duration", "metadata", "youtube_id", "method", "tab", "plex"}

func explainRules(verdict ...*queuestest.Rule) []*queuestest.Rule {
	base := []*queuestest.Rule{
		{Match: "from youtube_videos y", Cols: []string{"id", "title", "aired", "dq", "dur", "tab"}, Rows: [][]driver.Value{
			{"yt-remote", "طلع البدر الجزء الثاني", "2020-01-02", "exact", 600.0, "videos"}}},
		{Match: "from youtube_playlist_items", Cols: []string{"title", "position"}, Rows: [][]driver.Value{
			{"شمائل", int64(3)}}},
		{Match: "from videos v", Cols: explainVideoCols, Rows: [][]driver.Value{
			{videoV, "طلع البدر", "e1.mp4", libL, "قناة", "2020-01-01", "exact", 1500.5,
				[]byte(`{"width":1920,"height":1080}`), "yt-1", "info_json", "videos", "99123"}}},
	}
	return append(base, verdict...)
}

func TestExplain_ValidatesAndEscapes(t *testing.T) {
	f := newFixture()
	for _, tc := range []struct{ url string }{
		{"/api/explain"}, {"/api/explain?q=a"}, {"/api/explain?q=ab&library_id=nope"}, {"/api/explain?q=ab&limit=x"},
	} {
		if rec := f.do("GET", tc.url, "", viewer()); rec.Code != http.StatusBadRequest {
			t.Errorf("%s -> %d", tc.url, rec.Code)
		}
	}
	if rec := f.do("GET", "/api/explain?q=ab", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous -> %d", rec.Code)
	}
	if got := likePattern(`50%_off\x`); got != `%50\%\_off\\x%` {
		t.Errorf("likePattern = %q", got)
	}
}

func TestExplain_EpisodeVerdictWithPlaylistsAndPlex(t *testing.T) {
	f := newFixture(explainRules(&queuestest.Rule{Match: "from series_episodes e",
		Cols: []string{"sid", "series", "name", "season", "episode", "attached"},
		Rows: [][]driver.Value{{seasonA, seriesX, "كنوز السيرة", int64(2), int64(7), true}}})...)
	rec := f.do("GET", "/api/explain?q=%D8%B7%D9%84%D8%B9&library_id="+libL+"&limit=500", "", viewer())
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var out ExplainResponse
	decode(t, rec, &out)
	if out.Limit != explainMaxLimit || len(out.Videos) != 1 || len(out.Remote) != 1 {
		t.Fatalf("%+v", out)
	}
	v := out.Videos[0]
	if v.Verdict.Kind != VerdictEpisode || v.Verdict.Label != "كنوز السيرة S2 E7 (attached)" || !v.Verdict.Attached {
		t.Errorf("verdict %+v", v.Verdict)
	}
	if v.PlexKey == nil || *v.PlexKey != "99123" || v.LinkMethod == nil || *v.LinkMethod != "info_json" ||
		v.Width == nil || *v.Width != 1920 || len(v.Playlists) != 1 || *v.Playlists[0].Position != 3 {
		t.Errorf("video %+v", v)
	}
	if out.Remote[0].YouTubeID != "yt-remote" || len(out.Remote[0].Playlists) != 1 {
		t.Errorf("remote %+v", out.Remote[0])
	}
	// the search text is bound, never spliced into the SQL; the library filter rides along
	for _, c := range f.db.Find("from videos v") {
		if strings.Contains(c.SQL, "طلع") || c.Args[0] != "%طلع%" || c.Args[1] != libL || c.Args[2] != int64(explainMaxLimit) {
			t.Errorf("args %v / sql %s", c.Args, c.SQL)
		}
	}
}

func TestExplain_OtherVerdicts(t *testing.T) {
	norows := func(m string, cols ...string) *queuestest.Rule { return &queuestest.Rule{Match: m, Cols: cols} }
	episode := norows("from series_episodes e", "a", "b", "c", "d", "e", "f")
	cases := []struct {
		name  string
		rules []*queuestest.Rule
		kind  string
		label string
	}{
		{"extra", []*queuestest.Rule{episode, {Match: "from series_seasons ss where ss.retired_at is null and (ss.metadata",
			Cols: []string{"a", "b", "c", "d", "e"}, Rows: [][]driver.Value{{seasonA, seriesX, "الحبيب", int64(1), "extra"}}}},
			VerdictExtra, "extra (promo/recap) of الحبيب S1"},
		{"duplicate", []*queuestest.Rule{episode, {Match: "from series_seasons ss where ss.retired_at is null and (ss.metadata",
			Cols: []string{"a", "b", "c", "d", "e"}, Rows: [][]driver.Value{{seasonA, seriesX, "الحبيب", int64(1), "duplicate"}}}},
			VerdictDuplicate, "second upload of an episode in الحبيب S1"},
		{"clip group", []*queuestest.Rule{episode, norows("from series_seasons ss where ss.retired_at is null and (ss.metadata", "a", "b", "c", "d", "e"),
			{Match: "from clip_group_items", Cols: []string{"title"}, Rows: [][]driver.Value{{"مقاطع الصلاة"}}}},
			VerdictClipGroup, "clip in the group مقاطع الصلاة"},
		{"promo", []*queuestest.Rule{episode, norows("from series_seasons ss where ss.retired_at is null and (ss.metadata", "a", "b", "c", "d", "e"),
			norows("from clip_group_items", "title"),
			{Match: "from promo_links", Cols: []string{"name"}, Rows: [][]driver.Value{{"الحبيب"}}}},
			VerdictPromo, "promo of الحبيب"},
		{"unassigned", []*queuestest.Rule{episode, norows("from series_seasons ss where ss.retired_at is null and (ss.metadata", "a", "b", "c", "d", "e"),
			norows("from clip_group_items", "title"), norows("from promo_links", "name")},
			VerdictUnassigned, "unassigned (stand-alone)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(explainRules(tc.rules...)...)
			rec := f.do("GET", "/api/explain?q=طلع", "", viewer())
			var out ExplainResponse
			decode(t, rec, &out)
			if rec.Code != 200 || len(out.Videos) != 1 || out.Videos[0].Verdict.Kind != tc.kind || out.Videos[0].Verdict.Label != tc.label {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestExplain_AShortIsNeverAnEpisodeCandidate(t *testing.T) {
	rules := []*queuestest.Rule{
		{Match: "from youtube_videos y", Cols: []string{"id", "title", "aired", "dq", "dur", "tab"}},
		{Match: "from videos v", Cols: explainVideoCols, Rows: [][]driver.Value{
			{videoV, "clip", "c.mp4", libL, "L", nil, nil, 20.0, []byte(`{"width":720,"height":1280}`), nil, nil, nil, nil}}},
		{Match: "from series_episodes e", Cols: []string{"a", "b", "c", "d", "e", "f"}},
		{Match: "from series_seasons ss where ss.retired_at is null and (ss.metadata", Cols: []string{"a", "b", "c", "d", "e"}},
	}
	f := newFixture(rules...)
	rec := f.do("GET", "/api/explain?q=cl", "", viewer())
	var out ExplainResponse
	decode(t, rec, &out)
	if len(out.Videos) != 1 || out.Videos[0].Verdict.Kind != VerdictShort || out.Videos[0].Playlists == nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// ---------- style ----------

func TestStyle_RequiresAdmin(t *testing.T) {
	for _, p := range []string{"/api/seasons/" + seasonA + "/style", "/api/series/" + seriesX + "/style"} {
		for name, pr := range map[string]bool{"anonymous": false, "viewer": true} {
			f := newFixture()
			var rec = f.do("PUT", p, `{"style":"talk"}`, nil)
			if pr {
				rec = f.do("PUT", p, `{"style":"talk"}`, viewer())
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s -> %d", name, p, rec.Code)
			}
			for _, c := range f.db.Calls() {
				if c.Exec {
					t.Errorf("a forbidden request wrote: %s", c.SQL)
				}
			}
		}
	}
}

func TestStyle_PinSavesAndRequeuesRateStyleInOneTransaction(t *testing.T) {
	f := newFixture(
		&queuestest.Rule{Match: "from series_seasons where id =", Cols: []string{"one"}, Rows: [][]driver.Value{{int64(1)}}},
		enqueueRule([]driver.Value{int64(77), "inserted"}),
	)
	rec := f.do("PUT", "/api/seasons/"+seasonA+"/style", `{"style":"cinematic"}`, admin())
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var out StyleResponse
	decode(t, rec, &out)
	if out.Style == nil || *out.Style != "cinematic" || len(out.Seasons) != 1 || out.Seasons[0].Outcome != OutcomeInserted {
		t.Fatalf("%+v", out)
	}
	up := f.db.Find("insert into season_style")
	if len(up) != 1 || up[0].Args[0] != seasonA || up[0].Args[1] != "cinematic" {
		t.Fatalf("upsert %+v", up)
	}
	enq := f.db.Find("processing_jobs_enqueue")
	if len(enq) != 1 || enq[0].Args[0] != KindSeason || enq[0].Args[1] != seasonA || enq[0].Args[2] != "rate_style" ||
		enq[0].Args[4] != false || enq[0].Args[5] != false { // not forced: the fingerprint (it holds the override) decides
		t.Fatalf("enqueue %+v", enq)
	}
	if !strings.Contains(enq[0].Args[6].(string), "style_override") {
		t.Errorf("reason %v", enq[0].Args[6])
	}
	var order []string
	for _, c := range f.db.Calls() {
		switch {
		case c.SQL == "commit":
			order = append(order, "commit")
		case strings.Contains(c.SQL, "insert into season_style"):
			order = append(order, "upsert")
		case strings.Contains(c.SQL, "processing_jobs_enqueue"):
			order = append(order, "enqueue")
		}
	}
	if strings.Join(order, ",") != "upsert,enqueue,commit" {
		t.Errorf("order %v", order)
	}
}

func TestStyle_ClearAndValidation(t *testing.T) {
	f := newFixture(
		&queuestest.Rule{Match: "from series_seasons where id =", Cols: []string{"one"}, Rows: [][]driver.Value{{int64(1)}}},
		enqueueRule([]driver.Value{int64(78), "coalesced"}),
	)
	for _, body := range []string{`{"style":null}`, `{"style":""}`, `{}`} {
		if rec := f.do("PUT", "/api/seasons/"+seasonA+"/style", body, admin()); rec.Code != 200 {
			t.Fatalf("%s -> %d %s", body, rec.Code, rec.Body.String())
		}
	}
	for _, c := range f.db.Find("insert into season_style") {
		if c.Args[1] != nil {
			t.Errorf("clearing must store NULL, stored %v", c.Args[1])
		}
	}
	for _, body := range []string{`{"style":"neon"}`, `{"style":"talk","x":1}`, `nope`} {
		if rec := f.do("PUT", "/api/seasons/"+seasonA+"/style", body, admin()); rec.Code != http.StatusBadRequest {
			t.Errorf("%s -> %d", body, rec.Code)
		}
	}
	if rec := f.do("PUT", "/api/seasons/not-a-uuid/style", `{"style":"talk"}`, admin()); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id -> %d", rec.Code)
	}
}

func TestStyle_UnknownSeasonIs404AndNothingIsWritten(t *testing.T) {
	f := newFixture(&queuestest.Rule{Match: "from series_seasons where id =", Cols: []string{"one"}})
	if rec := f.do("PUT", "/api/seasons/"+seasonA+"/style", `{"style":"talk"}`, admin()); rec.Code != http.StatusNotFound {
		t.Fatalf("%d", rec.Code)
	}
	if len(f.db.Find("insert into season_style")) != 0 || len(f.db.Find("processing_jobs_enqueue")) != 0 {
		t.Error("a missing season was written")
	}
}

func TestStyle_WholeSeriesPinsEverySeason(t *testing.T) {
	f := newFixture(
		&queuestest.Rule{Match: "from series_seasons where series_id =", Cols: []string{"id"},
			Rows: [][]driver.Value{{seasonA}, {seasonB}}},
		enqueueRule([]driver.Value{int64(1), "inserted"}),
	)
	rec := f.do("PUT", "/api/series/"+seriesX+"/style", `{"style":"talk"}`, admin())
	var out StyleResponse
	decode(t, rec, &out)
	if rec.Code != 200 || len(out.Seasons) != 2 || len(f.db.Find("insert into season_style")) != 2 ||
		len(f.db.Find("processing_jobs_enqueue")) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestStyle_EnqueueFailureRollsBack(t *testing.T) {
	f := newFixture(
		&queuestest.Rule{Match: "from series_seasons where id =", Cols: []string{"one"}, Rows: [][]driver.Value{{int64(1)}}},
		&queuestest.Rule{Match: "processing_jobs_enqueue", Err: errors.New("boom")},
	)
	if rec := f.do("PUT", "/api/seasons/"+seasonA+"/style", `{"style":"talk"}`, admin()); rec.Code != http.StatusInternalServerError {
		t.Fatalf("%d", rec.Code)
	}
	var rolled, committed bool
	for _, c := range f.db.Calls() {
		rolled = rolled || c.SQL == "rollback"
		committed = committed || c.SQL == "commit"
	}
	if !rolled || committed {
		t.Errorf("rolled=%v committed=%v", rolled, committed)
	}
}

func TestSeasonStyles_List(t *testing.T) {
	f := newFixture(&queuestest.Rule{Match: "left join season_style st",
		Cols: []string{"sid", "series_id", "series", "name", "season", "lib", "libname", "style", "tier", "conf", "override"},
		Rows: [][]driver.Value{{seasonA, seriesX, "الحبيب", "الحبيب", int64(1), libL, "قناة", "talk", int64(1), 0.8, "cinematic"},
			{seasonB, seriesX, "الحبيب", "الحبيب", int64(2), nil, nil, nil, nil, nil, nil}}})
	rec := f.do("GET", "/api/seasons/styles?q=50%25&pinned=true&library_id="+libL, "", viewer())
	var out struct{ Items []SeasonStyle }
	decode(t, rec, &out)
	if rec.Code != 200 || len(out.Items) != 2 || *out.Items[0].OverrideStyle != "cinematic" || out.Items[1].Style != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	a := f.db.Find("left join season_style st")[0].Args
	if a[0] != libL || a[1] != `%50\%%` || a[2] != true {
		t.Errorf("args %v", a)
	}
	if rec = f.do("GET", "/api/seasons/styles?library_id=zzz", "", viewer()); rec.Code != http.StatusBadRequest {
		t.Errorf("bad library -> %d", rec.Code)
	}
}

// ---------- insights / pulse ----------

func TestInsights(t *testing.T) {
	f := newFixture(
		&queuestest.Rule{Match: "(select count(*) from libraries)", Cols: make([]string, 11), Rows: [][]driver.Value{
			{int64(3), int64(100), int64(90), int64(10), int64(20), int64(5), int64(4), int64(6), int64(2), int64(50), int64(7)}}},
		&queuestest.Rule{Match: "from season_style st", Cols: []string{"style", "n"},
			Rows: [][]driver.Value{{"talk", int64(8)}, {"cinematic", int64(2)}}},
		&queuestest.Rule{Match: "from season_tags t", Cols: []string{"genre", "n"},
			Rows: [][]driver.Value{{"aqeedah", int64(9)}, {"seerah", int64(4)}}},
	)
	rec := f.do("GET", "/api/queues/insights", "", viewer())
	var out Insights
	decode(t, rec, &out)
	if rec.Code != 200 || out.Totals.Videos != 100 || out.Totals.StylePinned != 2 || out.Totals.VideosWithoutDate != 7 ||
		out.Styles["talk"] != 8 || len(out.Genres) != 2 || out.Genres[0].Genre != "aqeedah" {
		t.Fatalf("%d %+v", rec.Code, out)
	}
	if rec = newFixture().do("GET", "/api/queues/insights", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous -> %d", rec.Code)
	}
}

func pulseFixture(running, waiting int64, beat, done any, paused bool, stuck ...[]driver.Value) *fixture {
	return newFixture(
		&queuestest.Rule{Match: "max(j.last_heartbeat_at)", Cols: []string{"r", "w", "hb"}, Rows: [][]driver.Value{{running, waiting, beat}}},
		&queuestest.Rule{Match: "select max(finished_at)", Cols: []string{"m"}, Rows: [][]driver.Value{{done}}},
		&queuestest.Rule{Match: "from queue_control", Cols: []string{"p"}, Rows: [][]driver.Value{{paused}}},
		&queuestest.Rule{Match: "j.claimed_by", Cols: []string{"id", "stage", "label", "kind", "sid", "hb", "by", "run"}, Rows: stuck},
	)
}

func TestPulse_States(t *testing.T) {
	recent := t0.Add(-5 * time.Second)
	old := t0.Add(-30 * time.Minute)
	cases := []struct {
		name string
		f    *fixture
		want string
	}{
		{"idle", pulseFixture(0, 0, nil, nil, false), PulseIdle},
		{"working", pulseFixture(2, 5, recent, recent, false), PulseWorking},
		{"queued work, nothing for 30 min", pulseFixture(0, 5, nil, old, false), PulseStalled},
		{"queued work and nothing ever", pulseFixture(0, 5, nil, nil, false), PulseStalled},
		{"paused", pulseFixture(0, 5, nil, old, true), PulsePaused},
		{"a job lost its heartbeat", pulseFixture(1, 0, old, recent, false,
			[]driver.Value{int64(9), "probe", "فيديو", "video", videoV, 1800.0, "pc/1", 2000.0}), PulseStalled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.f.do("GET", "/api/queues/pulse", "", viewer())
			var out Pulse
			decode(t, rec, &out)
			if rec.Code != 200 || out.State != tc.want {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if tc.want == PulseStalled && strings.Contains(tc.name, "heartbeat") &&
				(len(out.Stuck) != 1 || out.Stuck[0].Stage != "probe" || out.Stuck[0].HeartbeatAgeSec != 1800) {
				t.Errorf("stuck %+v", out.Stuck)
			}
		})
	}
}

func TestPulse_ReportsTheAgeOfTheLastActivity(t *testing.T) {
	f := pulseFixture(1, 0, t0.Add(-12*time.Second), t0.Add(-90*time.Second), false)
	rec := f.do("GET", "/api/queues/pulse", "", viewer())
	var raw map[string]json.RawMessage
	decode(t, rec, &raw)
	var out Pulse
	decode(t, rec, &out)
	if out.AgeSec == nil || *out.AgeSec != 12 || out.Stuck == nil {
		t.Fatalf("%s", rec.Body.String())
	}
}

// ---------- library collections ----------

func TestLibraryCollections(t *testing.T) {
	f := newFixture(
		&queuestest.Rule{Match: "from topic_collections t", Cols: []string{"id", "name", "n", "coll"},
			Rows: [][]driver.Value{{"t1", "السيرة", int64(12), "قناة - ملف - السيرة"}, {"t2", "الفقه", int64(3), nil}}},
		&queuestest.Rule{Match: "from clip_groups g", Cols: []string{"id", "kind", "basis", "title", "n", "coll"},
			Rows: [][]driver.Value{{"g1", "clips", "topic", "مقاطع", int64(40), nil}}},
		&queuestest.Rule{Match: "from promo_links p", Cols: []string{"vid", "title", "show", "sid", "season", "ev"},
			Rows: [][]driver.Value{{videoV, "برومو الحبيب", "الحبيب", seasonA, int64(2), "title names the show"}}},
	)
	rec := f.do("GET", "/api/items/library/"+libL+"/collections", "", viewer())
	var out LibraryCollectionsResponse
	decode(t, rec, &out)
	if rec.Code != 200 || len(out.Topics) != 2 || out.Topics[0].Collection == nil || out.Topics[1].Collection != nil ||
		len(out.ClipGroups) != 1 || out.ClipGroups[0].Items != 40 ||
		len(out.Promos) != 1 || *out.Promos[0].Season != 2 || *out.Promos[0].SeasonID != seasonA {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	for _, c := range f.db.Calls() {
		if len(c.Args) > 0 && c.Args[0] != libL {
			t.Errorf("not scoped to the library: %v", c.Args)
		}
	}
	if rec = f.do("GET", "/api/items/library/nope/collections", "", viewer()); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id -> %d", rec.Code)
	}
	if rec = f.do("GET", "/api/items/library/"+libL+"/collections", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous -> %d", rec.Code)
	}
}
