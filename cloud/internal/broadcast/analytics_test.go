package broadcast

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The analytics events' columns are the edge's (testdata/events_schema.json, which the edge's
// T01 checks too), so the rollup reads what the edge writes.
func TestEventsSchema(t *testing.T) {
	raw, err := os.ReadFile("testdata/events_schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Dataset string   `json:"dataset"`
		Blobs   []string `json:"blobs"`
		Doubles []string `json:"doubles"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Dataset != EventsDataset || !reflect.DeepEqual(s.Blobs, blobColumns) || !reflect.DeepEqual(s.Doubles, doubleColumns) {
		t.Fatalf("the Go columns differ from events_schema.json: %+v", s)
	}
	if col("publisher") != "blob2" || col("minutes") != "double2" || col("glass_to_glass_ms") != "double8" {
		t.Fatal("col maps names to the wrong positions")
	}
}

var hour = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// an hour of events as the SQL API answers them (64-bit sums quoted, as ClickHouse does)
func hourRows() []Row {
	return []Row{
		{"event": "play", "publisher": "p1", "channel": "c1", "country": "EG", "device": "web", "n": "10", "startup_ms": 9000.0},
		{"event": "play", "publisher": "p1", "channel": "c1", "country": "EG", "device": "tvos", "n": "2", "startup_ms": 2000.0},
		{"event": "play", "publisher": "p1", "channel": "c1", "country": "DE", "device": "web", "n": "2", "startup_ms": 1000.0},
		{"event": "beat", "publisher": "p1", "channel": "c1", "country": "EG", "device": "web", "rung": "720p", "n": "300", "minutes": 300.0, "rebuffer_ms": 180000.0},
		{"event": "beat", "publisher": "p1", "channel": "c1", "country": "DE", "device": "web", "rung": "480p", "n": "100", "minutes": 100.0, "rebuffer_ms": 60000.0},
		{"event": "error", "publisher": "p1", "channel": "c1", "code": "storage_unavailable", "n": "3"},
		{"event": "blocked", "publisher": "p1", "channel": "c1", "code": "full", "n": "4"},
		{"event": "ingest", "publisher": "p1", "channel": "c1", "rung": "720p", "n": "900", "segment_bytes": 9e8},
		{"event": "ingest", "publisher": "p1", "channel": "c1", "rung": "480p", "n": "900", "segment_bytes": 4e8},
		{"event": "beat", "publisher": "p2", "channel": "c9", "rung": "src", "n": "60", "minutes": 60.0},
		{"event": "play", "publisher": "", "channel": "c1", "n": "1"}, // no publisher: dropped
	}
}

// T02: an hour of events rolls up into one row per publisher and channel.
func TestAggregate(t *testing.T) {
	peaks := []Row{
		{"publisher": "p1", "channel": "c1", "minute": "2026-11-14 10:00:00", "n": "7"},
		{"publisher": "p1", "channel": "c1", "minute": "2026-11-14 10:01:00", "n": "11"},
		{"publisher": "p2", "channel": "c9", "minute": "2026-11-14 10:01:00", "n": "1"},
	}
	hs := Aggregate(hour, hourRows(), peaks)
	if len(hs) != 2 {
		t.Fatalf("rows %d, want 2: %+v", len(hs), hs)
	}
	h := hs[0]
	if h.Publisher != "p1" || h.Channel != "c1" || !h.Hour.Equal(hour) {
		t.Fatalf("first row %+v", h)
	}
	if h.Sessions != 14 || h.ViewerMinutes != 400 || h.PeakConcurrent != 11 || h.StartupMsSum != 12000 {
		t.Fatalf("totals %+v", h)
	}
	if h.Countries["EG"] != 12 || h.Countries["DE"] != 2 || h.Devices["web"] != 12 || h.Devices["tvos"] != 2 {
		t.Fatalf("countries %v devices %v", h.Countries, h.Devices)
	}
	if h.Quality["720p"] != 300 || h.Quality["480p"] != 100 {
		t.Fatalf("quality %v", h.Quality)
	}
	// 240 s of rebuffering in 400 viewer-minutes
	if want := 240000.0 / (400 * 60000); h.RebufferRatio != want {
		t.Fatalf("rebuffer %v, want %v", h.RebufferRatio, want)
	}
	if h.Errors != 3 || h.ErrorsByCode["storage_unavailable"] != 3 || h.Blocked["full"] != 4 {
		t.Fatalf("errors %v blocked %v", h.ErrorsByCode, h.Blocked)
	}
	if h.Segments != 1800 || h.SegmentBytes != 1.3e9 || h.Rungs != 2 {
		t.Fatalf("ingest %+v", h)
	}
	if hs[1].ViewerMinutes != 60 || hs[1].Quality["src"] != 60 || hs[1].PeakConcurrent != 1 {
		t.Fatalf("second row %+v", hs[1])
	}
}

// T02 and the first acceptance criterion: 12 sessions from Egypt and 2 from Germany show Egypt
// 12 and Other 2; Germany isn't named. A publisher with one viewer sees only "other".
func TestFoldAtK5(t *testing.T) {
	got := Fold(map[string]float64{"EG": 12, "DE": 2}, 5)
	want := []Count{{Key: "EG", Value: 12}, {Key: OtherKey, Value: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fold = %v, want %v", got, want)
	}
	if got := Fold(map[string]float64{"EG": 5, "DE": 4.99, "FR": 1}, 5); !reflect.DeepEqual(got, []Count{{"EG", 5}, {OtherKey, 5.99}}) {
		t.Fatalf("Fold at the edge = %v", got)
	}
	if got := Fold(map[string]float64{"web": 1}, 5); !reflect.DeepEqual(got, []Count{{OtherKey, 1}}) {
		t.Fatalf("one viewer = %v", got)
	}
	if got := Fold(map[string]float64{}, 5); len(got) != 0 {
		t.Fatalf("nothing = %v", got)
	}
}

// A caller chooses a period's edges (tz moves a day by whole hours, today grows each hour), so a
// fold over the whole period would leak: [a,b) and [a,b+1h) each folded as a whole name Germany
// 15 and 17, and their difference is the 2 sessions from Germany in the added hour, a group under
// k. Folded hour by hour, no difference of two periods recovers a group under k: for every pair
// of nested windows over these hours, each country's named count differs by 0 or by at least k,
// and every named count is at least k.
func TestHourFoldOverlappingWindowsRecoverNoSmallGroup(t *testing.T) {
	const k = 5
	hours := []map[string]float64{
		{"EG": 12, "DE": 5},
		{"EG": 12, "DE": 5, "FR": 1},
		{"EG": 12, "DE": 5},
		{"EG": 12, "DE": 2}, // the hour the overlapping window adds: Germany 2, under k
		{"EG": 3, "DE": 7, "FR": 4},
		{"EG": 12, "FR": 6, "JO": 1},
	}
	period := func(from, to int) map[string]float64 {
		h := newHourFold(k)
		for _, cell := range hours[from:to] {
			h.add(cell)
		}
		m := map[string]float64{}
		for _, c := range h.counts() {
			m[c.Key] = c.Value
		}
		return m
	}

	// the finding's own case: [0,3) and [0,4), one hour apart
	a, b := period(0, 3), period(0, 4)
	if d := b["DE"] - a["DE"]; d != 0 {
		t.Fatalf("[0,4) - [0,3) names Germany %v: the 2 sessions of the added hour leak", d)
	}
	if b["DE"] != 15 || b[OtherKey]-a[OtherKey] != 2 {
		t.Fatalf("Germany %v, other grew by %v; want 15 and the 2 sessions unnamed in other", b["DE"], b[OtherKey]-a[OtherKey])
	}
	// the old fold, over the whole window, would have named them
	whole := func(from, to int) map[string]float64 {
		sum := map[string]float64{}
		for _, cell := range hours[from:to] {
			for key, v := range cell {
				sum[key] += v
			}
		}
		m := map[string]float64{}
		for _, c := range Fold(sum, k) {
			m[c.Key] = c.Value
		}
		return m
	}
	if d := whole(0, 4)["DE"] - whole(0, 3)["DE"]; d != 2 {
		t.Fatalf("the whole-window fold's difference = %v; the fixture should show its leak (2)", d)
	}

	// every pair of nested windows [i,j) ⊂ [i2,j2), every country
	for i := 0; i < len(hours); i++ {
		for j := i + 1; j <= len(hours); j++ {
			inner := period(i, j)
			for key, v := range inner {
				if key != OtherKey && v < k {
					t.Fatalf("[%d,%d) names %s with %v < k", i, j, key, v)
				}
			}
			for i2 := 0; i2 <= i; i2++ {
				for j2 := j; j2 <= len(hours); j2++ {
					outer := period(i2, j2)
					for _, key := range []string{"EG", "DE", "FR", "JO"} {
						if d := outer[key] - inner[key]; d != 0 && d < k {
							t.Fatalf("[%d,%d) - [%d,%d) recovers %s = %v, under k", i2, j2, i, j, key, d)
						}
					}
				}
			}
		}
	}
}

// T03: the daily check is quiet at 1 % and raises at 3 %, and small numbers are noise.
func TestCompare(t *testing.T) {
	metered := map[string]float64{"quiet": 10000, "loud": 10000, "tiny": 100, "only-metered": 500}
	rollup := map[string]float64{"quiet": 10100, "loud": 9700, "tiny": 105, "only-rolled": 20}
	ms := Compare(rollup, metered)
	var names []string
	for _, m := range ms {
		names = append(names, m.Publisher)
	}
	if !reflect.DeepEqual(names, []string{"loud", "only-metered", "only-rolled"}) {
		t.Fatalf("mismatches %v", names)
	}
	if ms[0].Ratio < 0.029 || ms[0].Ratio > 0.031 {
		t.Fatalf("ratio %v", ms[0].Ratio)
	}
}

// The SQL client: the query as the body, the token as the bearer, quoted numbers read, and a
// failure is ErrAnalyticsUnavailable (the rollup tries again later).
func TestSQLClient(t *testing.T) {
	var gotSQL, gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotSQL, gotAuth, gotPath = string(b), r.Header.Get("Authorization"), r.URL.Path
		if strings.Contains(gotSQL, "FAIL") {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"meta":[],"data":[{"event":"beat","n":"12345678901","minutes":3.5}],"rows":1}`))
	}))
	defer srv.Close()
	c := &SQLClient{BaseURL: srv.URL, AccountID: "acct", Token: "tok"}
	rows, err := c.Query(context.Background(), hourQuery(hour, hour.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/accounts/acct/analytics_engine/sql" || gotAuth != "Bearer tok" {
		t.Fatalf("path %s auth %s", gotPath, gotAuth)
	}
	for _, want := range []string{"FROM broadcast_events", "_sample_interval", "toDateTime('2026-11-14 10:00:00')", "blob2 AS publisher", "double2"} {
		if !strings.Contains(gotSQL, want) {
			t.Errorf("query lacks %q:\n%s", want, gotSQL)
		}
	}
	if rows[0].Num("n") != 12345678901 || rows[0].Num("minutes") != 3.5 || rows[0].Str("event") != "beat" {
		t.Fatalf("rows %v", rows)
	}
	if _, err := c.Query(context.Background(), "FAIL"); !errors.Is(err, ErrAnalyticsUnavailable) {
		t.Fatalf("a 502 is %v", err)
	}
	if _, err := (&SQLClient{}).Query(context.Background(), "x"); !errors.Is(err, ErrAnalyticsUnavailable) {
		t.Fatalf("no account is %v", err)
	}
}

func TestQueriesWeightBySample(t *testing.T) {
	for name, q := range map[string]string{
		"hour":   hourQuery(hour, hour.Add(time.Hour)),
		"peak":   peakQuery(hour, hour.Add(time.Hour)),
		"minute": minuteQuery(hour, hour.Add(time.Minute)),
	} {
		if strings.Contains(q, "COUNT(") || !strings.Contains(q, "SUM(_sample_interval") {
			t.Errorf("%s query isn't weighted by _sample_interval:\n%s", name, q)
		}
	}
	if !strings.Contains(minuteQuery(hour, hour), "IF(double7 <= 3000, _sample_interval, 0)") {
		t.Error("the minute query lacks the 3 s lag bucket")
	}
}

func TestCostEstimate(t *testing.T) {
	// README §14.4: Broadcaster, 3 channels × 3 rungs always on, 5,000 h ≈ $46
	got := CostEstimate(5000*60, map[int]float64{3: 3 * 730})
	if got < 45 || got > 52 {
		t.Fatalf("cost %v, want about 46 to 51", got)
	}
	if CostEstimate(0, nil) != CostWorkersPaidUSD {
		t.Fatal("an idle month costs Workers Paid")
	}
	if ChannelHourCost(0) != ChannelHourCost(1) {
		t.Fatal("a channel has at least one rung")
	}
}

func TestPeriod(t *testing.T) {
	cairo, _ := time.LoadLocation("Africa/Cairo")
	now := time.Date(2026, 11, 14, 23, 30, 0, 0, time.UTC) // already the 15th in Cairo
	from, to, err := Period("today", "", cairo, now)
	if err != nil || from.In(cairo).Day() != 15 || to.Sub(from) != 24*time.Hour {
		t.Fatalf("today %v–%v %v", from, to, err)
	}
	from, _, _ = Period("7d", "", cairo, now)
	if from.In(cairo).Day() != 9 {
		t.Fatalf("7d starts %v", from.In(cairo))
	}
	from, to, _ = Period("month", "", cairo, now)
	if from.In(cairo).Day() != 1 || to.In(cairo).Month() != time.December {
		t.Fatalf("month %v–%v", from, to)
	}
	if _, _, err := Period("day", "2026-11-03", cairo, now); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][2]string{{"day", "nope"}, {"day", "2024-01-01"}, {"day", "2027-01-01"}, {"year", ""}} {
		if _, _, err := Period(bad[0], bad[1], cairo, now); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// T06 (the series' shape): every maktaba_broadcast_* series 53.17 names, and the revocation
// list's age only once the edge took one.
func TestWriteSeries(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	s := Snapshot{ChannelsLive: 2, Viewers: 40, ViewerMinutesTotal: 1234, LagBuckets: []float64{1, 2, 3, 4, 5, 6, 7, 8}, LagCount: 9, LagSumSeconds: 11.5, CostUSD: 12.25}
	var b bytes.Buffer
	if err := WriteSeries(&b, s, sql.NullTime{}, 0, now); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"maktaba_broadcast_channels_live 2",
		"maktaba_broadcast_viewers 40",
		"maktaba_broadcast_viewer_minutes_total 1234",
		`maktaba_broadcast_ingest_lag_seconds_bucket{le="3"} 5`,
		`maktaba_broadcast_ingest_lag_seconds_bucket{le="0.25"} 1`,
		`maktaba_broadcast_ingest_lag_seconds_bucket{le="+Inf"} 9`,
		"maktaba_broadcast_ingest_lag_seconds_count 9",
		"maktaba_broadcast_edge_cost_estimate_usd 12.25",
		"# TYPE maktaba_broadcast_ingest_lag_seconds histogram",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "revocation_list_age") || strings.Contains(out, "monthly_budget") {
		t.Errorf("series that should be absent:\n%s", out)
	}
	b.Reset()
	_ = WriteSeries(&b, s, sql.NullTime{Time: now.Add(-11 * time.Minute), Valid: true}, 100, now)
	if !strings.Contains(b.String(), "maktaba_broadcast_revocation_list_age_seconds 660") || !strings.Contains(b.String(), "maktaba_broadcast_monthly_budget_usd 100") {
		t.Errorf("age or budget missing:\n%s", b.String())
	}
}

func TestWriteCSV(t *testing.T) {
	peak, sess := 11.0, 14.0
	r := Report{From: hour, To: hour.Add(24 * time.Hour), TimeZone: "UTC", PeriodHours: 6.67, Peak: &peak, Sessions: &sess,
		Countries: []Count{{"EG", 12}, {OtherKey, 2}}, Daily: []Day{{Date: "2026-11-14", ViewerMinutes: 400, Sessions: 14, Peak: 11}}}
	var b bytes.Buffer
	if err := WriteCSV(&b, r); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"country,EG,12", "country,other,2", "summary,peak_concurrent,11", "2026-11-14,400,14,11,0"} {
		if !strings.Contains(out, want) {
			t.Errorf("csv lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DE") {
		t.Error("a folded country is named in the CSV")
	}
}

func TestHashIDMatchesTheEdge(t *testing.T) {
	// web/packages/broadcast-edge/src/log.ts's hashId("pub-1") (FNV-1a 64)
	if got := HashID(""); got != "cbf29ce484222325" {
		t.Fatalf("HashID(\"\") = %s", got)
	}
	if HashID("pub-1") == "pub-1" || len(HashID("pub-1")) != 16 {
		t.Fatal("not a 16-digit hash")
	}
}

// FoldMinute: a minute's rows into the snapshot before it; the counters only grow, the gauges are
// the minute's, and a snapshot without buckets (the first poll) starts them.
func TestFoldMinute(t *testing.T) {
	m := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	ingest := Row{"event": "ingest", "publisher": "p", "channel": "c", "n": 4.0, "lag_ms": 9000.0}
	for i, le := range LagBuckets {
		v := 3.0
		if le >= 5 {
			v = 4
		}
		ingest[LagColumn(i)] = v
	}
	rows := []Row{ingest, {"event": "beat", "publisher": "p", "channel": "c", "n": "7"}, {"event": "beat", "publisher": "p", "channel": "d", "n": 2.0}}
	s, viewers := FoldMinute(Snapshot{ViewerMinutesTotal: 10, CostUSD: 3}, rows, m)
	if s.Minute != m || s.ChannelsLive != 1 || s.Viewers != 9 || s.ViewerMinutesTotal != 19 || s.LagCount != 4 || s.LagSumSeconds != 9 {
		t.Errorf("snapshot = %+v", s)
	}
	if s.CostUSD != 3 {
		t.Errorf("FoldMinute set the cost (the caller's): %g", s.CostUSD)
	}
	if viewers[LiveChannel{"p", "c"}] != 7 || viewers[LiveChannel{"p", "d"}] != 2 {
		t.Errorf("viewers = %v", viewers)
	}
	prev := s
	s, _ = FoldMinute(prev, []Row{ingest}, m.Add(time.Minute))
	if s.Viewers != 0 || s.ViewerMinutesTotal != 19 || s.LagCount != 8 || s.LagBuckets[len(LagBuckets)-1] != 8 || s.LagBuckets[0] != 6 {
		t.Errorf("second minute = %+v", s)
	}
	if prev.LagBuckets[0] != 3 {
		t.Errorf("FoldMinute changed the snapshot it was given: %v", prev.LagBuckets)
	}
}
