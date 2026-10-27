package videos

// Epic 45 Phase 3 (story 45.20) on GET /api/videos: duration, added and
// resolution.

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

func TestListBandFilters(t *testing.T) {
	db := extDB(t)
	for _, q := range []string{
		`CREATE TABLE media_info (video_id TEXT PRIMARY KEY, width INTEGER, height INTEGER)`,
		`INSERT INTO media_info VALUES ('` + xvid(1) + `',1920,1080), ('` + xvid(2) + `',1280,720),
			('` + xvid(3) + `',640,480), ('` + xvid(8) + `',3840,2160)`,
		`UPDATE videos SET duration_sec = 3600 WHERE id = '` + xvid(1) + `'`,
		`UPDATE videos SET duration_sec = 1800 WHERE id = '` + xvid(2) + `'`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// the fixture's videos were added on 2026-10-01; charlie (3) at the start of the year
	if _, err := db.Exec(`UPDATE videos SET created_at = $1 WHERE id = $2`,
		time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), xvid(3)); err != nil {
		t.Fatal(err)
	}
	h := &Handler{DB: db, NowFunc: func() time.Time { return time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC) }}
	ctx := context.Background()
	q := func(kv ...string) url.Values {
		v := url.Values{"sort": {"title"}, "order": {"asc"}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	for _, c := range []struct {
		name string
		qs   url.Values
		want []string
	}{
		// library A: 1 is an hour, 2 half an hour, 4 and 5 have no length
		{"long", q("duration", "long"), []string{xvid(1)}},
		{"medium", q("duration", "medium"), []string{xvid(2)}},
		{"short", q("duration", "short"), []string{xvid(3), xvid(6), xvid(7)}},
		{"added this week", q("added", "week"), []string{xvid(1), xvid(2), xvid(4), xvid(5), xvid(6), xvid(7)}},
		{"added this year", q("added", "year"), []string{xvid(1), xvid(2), xvid(3), xvid(4), xvid(5), xvid(6), xvid(7)}},
		{"full HD", q("resolution", "fhd"), []string{xvid(1)}},
		{"HD", q("resolution", "hd"), []string{xvid(2)}},
		{"SD and short", q("resolution", "sd", "duration", "short"), []string{xvid(3)}},
		// library B's 4K video is never the viewer of A's
		{"4K", q("resolution", "uhd"), []string{}},
	} {
		code, items, _ := listBody(ctx, t, h, viewerA(), c.qs)
		if code != http.StatusOK {
			t.Errorf("%s: status %d", c.name, code)
			continue
		}
		if got := vidIDs(items); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if code, items, _ := listBody(ctx, t, h, admin(), q("resolution", "uhd")); code != http.StatusOK || !slices.Equal(vidIDs(items), []string{xvid(8)}) {
		t.Errorf("admin 4K: %d %v", code, vidIDs(items))
	}
	for name, qs := range map[string]url.Values{
		"bad duration":   q("duration", "tiny"),
		"bad added":      q("added", "today"),
		"bad resolution": q("resolution", "8k"),
	} {
		if code, _, _ := listBody(ctx, t, h, viewerA(), qs); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
}
