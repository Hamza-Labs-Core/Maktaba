package videos

import (
	"net/url"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// sort=plays on Postgres: the count runs over the listed videos only (the
// filters and the library scope inside it) and pages as on SQLite. Runs only
// with MAKTABA_TEST_PG_URL.
func TestListPlaysPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	const someone = "00000000-0000-4000-8000-0000000000f2"
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id, username, pw_hash) VALUES ($1, 'plays-me', 'x'), ($2, 'plays-someone', 'x')`, extUser, someone)
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'A', '{/m/a}'), ($2, 'B', '{/m/b}')`, extLibA, extLibB)
	for _, v := range extFixture() {
		var dur any
		if v.duration != nil {
			dur = *v.duration
		}
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec)
			VALUES ($1, $2, $3, $3, encode(sha256(convert_to($4, 'UTF8')), 'hex'), 0, now(), 'ready', $5, $6)`, v.id, v.lib, "/m/"+v.id, "h"+v.id, v.title, dur)
		for j := 0; j < v.plays; j++ {
			exec(`INSERT INTO watch_sessions (id, user_id, video_id) VALUES (gen_random_uuid(), $1, $2)`, someone, v.id)
		}
	}
	exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed) VALUES ($1, $2, 0, true), ($1, $3, 40, false), ($1, $4, 0, true)`,
		extUser, xvid(1), xvid(2), xvid(8))
	h := &Handler{DB: db}
	rows := extFixture()
	for _, tc := range []struct {
		name string
		p    *principal.Principal
		qs   url.Values
		want []string
	}{
		{"admin", admin(), url.Values{}, expectedOrder(rows, "plays", "desc")},
		{"admin, asc", admin(), url.Values{"order": {"asc"}}, expectedOrder(rows, "plays", "asc")},
		{"viewer of A", viewerA(), url.Values{}, expectedOrder(rows[:7], "plays", "desc")},
		{"viewer of A, unwatched", viewerA(), url.Values{"watched": {"unwatched"}}, expectedOrder(rows[2:7], "plays", "desc")},
		{"admin, q", admin(), url.Values{"q": {"o"}}, expectedOrder([]extVideo{rows[1], rows[4], rows[5], rows[6], rows[7]}, "plays", "desc")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.qs.Set("sort", "plays")
			if got := extWalk(t, h, tc.p, tc.qs); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
