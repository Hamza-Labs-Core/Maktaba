package browse

import (
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The home's rows can read slot 0165's indexes: their predicate is the
// indexes' (a change to visible would leave them unused, and the home back
// to sorting every video). Runs only with MAKTABA_TEST_PG_URL.
func TestHomeRowsUseTheirIndexesPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// an empty table: without this the planner rightly prefers a scan
	if _, err := conn.ExecContext(t.Context(), `SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	const lib = "00000000-0000-4000-8000-00000000000a"
	for _, c := range []struct{ name, q, index string }{
		{"recently added", `SELECT ` + cardColumns + ` FROM videos v WHERE ` + visible + `
			ORDER BY v.created_at DESC, v.mtime DESC, v.id DESC LIMIT 300`, "videos_recent_idx"},
		{"a library's recently added", `SELECT ` + cardColumns + ` FROM videos v WHERE ` + visible + `
			AND v.library_id = '` + lib + `' ORDER BY v.created_at DESC, v.mtime DESC, v.id DESC LIMIT 300`, "videos_library_recent_idx"},
		{"featured series", `SELECT s.id FROM series s
			WHERE EXISTS (SELECT 1 FROM series_episodes se JOIN videos v ON v.id = se.video_id
			               WHERE se.series_id = s.id AND ` + visible + `)
			ORDER BY s.created_at DESC, s.id DESC LIMIT 1`, "series_created_idx"},
	} {
		rows, err := conn.QueryContext(t.Context(), `EXPLAIN `+c.q)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var plan []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, line)
		}
		_ = rows.Close()
		if p := strings.Join(plan, "\n"); !strings.Contains(p, c.index) || strings.Contains(p, "Sort Key: v.created_at") {
			t.Errorf("%s does not read %s in order:\n%s", c.name, c.index, p)
		}
	}
}
