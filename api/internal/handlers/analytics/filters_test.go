package analytics

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseFilters(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	const lib = "6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"
	const user = "7a1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"

	req := httptest.NewRequest("GET", "/x?range=30d&library="+lib+"&user="+user+"&client_app=Player&surface=video", nil)
	f, e := ParseFilters(req, now)
	if e != nil {
		t.Fatal(e)
	}
	if f.Range.Label != "30d" || f.Library != lib || f.User != user || f.ClientApp != "player" || f.Surface != "video" {
		t.Errorf("filters: %+v", f)
	}
	if !f.wantPlays() || f.wantViewings() {
		t.Error("a library or the video surface keeps plays only")
	}

	// A custom range: a date "to" runs to the end of that day.
	req = httptest.NewRequest("GET", "/x?from=2026-10-01&to=2026-10-03", nil)
	f, e = ParseFilters(req, now)
	if e != nil {
		t.Fatal(e)
	}
	if f.Range.Label != "custom" || !f.Range.Start.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) ||
		!f.Range.End.Equal(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("custom range: %+v", f.Range)
	}
	if f.Range.short(now) {
		t.Error("three days is not short")
	}
	req = httptest.NewRequest("GET", "/x?from=2026-10-01T10:00:00Z", nil)
	if f, e = ParseFilters(req, now); e != nil || f.Range.HasUpperBound() || f.Range.Start.Hour() != 10 {
		t.Errorf("from alone: %+v %v", f.Range, e)
	}
	if f.wantPlays() != true || f.wantViewings() != true {
		t.Error("no filter keeps both")
	}

	for _, bad := range []string{
		"from=yesterday", "to=2026-13-01", "from=2026-10-05&to=2026-10-01",
		"library=nope", "user=1", "client_app=" + "Has%20Space", "surface=plex",
	} {
		req := httptest.NewRequest("GET", "/x?"+bad, nil)
		if _, e := ParseFilters(req, now); e == nil || e.Status != 400 {
			t.Errorf("%s: want 400, got %v", bad, e)
		}
	}

	req = httptest.NewRequest("GET", "/x?surface=tuner", nil)
	f, _ = ParseFilters(req, now)
	if f.wantPlays() || !f.wantViewings() {
		t.Error("a TV surface keeps viewings only")
	}
}

func TestFiltersKeyDiffers(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	a, _ := ParseFilters(httptest.NewRequest("GET", "/x?range=7d", nil), now)
	b, _ := ParseFilters(httptest.NewRequest("GET", "/x?range=7d&surface=app", nil), now)
	c, _ := ParseFilters(httptest.NewRequest("GET", "/x?range=7d", nil), now)
	if a.key() == b.key() || a.key() != c.key() {
		t.Errorf("keys: %q %q %q", a.key(), b.key(), c.key())
	}
}

func TestPeak(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 10, 26, 20, m, 0, 0, time.UTC) }
	if n, _ := peak(nil); n != 0 {
		t.Errorf("no viewings: %d", n)
	}
	n, when := peak([]interval{
		{at(0), at(30)},
		{at(10), at(20)},
		{at(15), at(40)},
		{at(30), at(50)}, // starts as the first ends: not with it
		{at(45), at(45)}, // a zero-length viewing still counts
	})
	if n != 3 || !when.Equal(at(15)) {
		t.Errorf("peak %d at %v, want 3 at 20:15", n, when)
	}
	if n, _ := peak([]interval{{at(5), at(5)}}); n != 1 {
		t.Errorf("one instant viewing: %d", n)
	}
}

func TestDBTimeScan(t *testing.T) {
	want := time.Date(2026, 10, 26, 12, 30, 5, 0, time.UTC)
	for _, v := range []any{
		want,
		"2026-10-26T12:30:05.000Z",
		"2026-10-26 12:30:05",
		"2026-10-26 15:30:05+03:00",
		[]byte("2026-10-26T12:30:05Z"),
		"2026-10-26 12:30:05 +0000 UTC",
	} {
		var d dbTime
		if err := d.Scan(v); err != nil || !d.Valid || !d.T.Equal(want) {
			t.Errorf("%v: %v %v", v, d, err)
		}
	}
	var d dbTime
	if err := d.Scan(nil); err != nil || d.Valid || d.String() != "" {
		t.Errorf("nil: %v %v", d, err)
	}
	if err := d.Scan("not a time"); err == nil {
		t.Error("garbage should not scan")
	}
}
