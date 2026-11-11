package videotitle

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/testpg"
)

var stripCases = []struct{ in, want string }{
	{"My Lecture 03.mp4", "My Lecture 03"},
	{"a.b.mkv", "a.b"},
	{"clip.MP4", "clip"},
	{"talk.m4v", "talk"},
	{"محاضرة الفقه.mkv", "محاضرة الفقه"},
	{"notes", "notes"},             // no extension
	{".mp4", ".mp4"},               // a dotfile: no stem
	{"x.", "x."},                   // an empty extension
	{"", ""},                       // nothing at all
	{"Ch. 3 intro", "Ch. 3 intro"}, // a dot inside the name, no extension
	{"..mp4", "."},                 // the stem is a dot
	{".hidden.mkv", ".hidden"},
}

func TestStripExt(t *testing.T) {
	for _, c := range stripCases {
		if got := StripExt(c.in); got != c.want {
			t.Errorf("StripExt(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDisplay(t *testing.T) {
	cases := []struct{ title, filename, want string }{
		{"Real Title", "file.mp4", "Real Title"},
		{"", "My Lecture 03.mp4", "My Lecture 03"},
		{"   ", "My Lecture 03.mp4", "My Lecture 03"},
		{"Title.mp4", "file.mp4", "Title.mp4"}, // a metadata title is never cut
	}
	for _, c := range cases {
		if got := Display(c.title, c.filename); got != c.want {
			t.Errorf("Display(%q, %q) = %q, want %q", c.title, c.filename, got, c.want)
		}
	}
}

// TestSQLMatchesGo runs the SQL form on SQLite and checks it against the Go
// rule for every case, with and without a title. Postgres runs the same text
// in the handlers' Postgres tests.
func TestSQLMatchesGo(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE v (title TEXT, filename TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, c := range stripCases {
		for _, title := range []any{nil, "", " ", "Kept.mkv"} {
			if _, err := db.Exec(`DELETE FROM v`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO v VALUES (?, ?)`, title, c.in); err != nil {
				t.Fatal(err)
			}
			var got sql.NullString
			if err := db.QueryRow(`SELECT ` + SQL("title", "filename") + ` FROM v`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			ts, _ := title.(string)
			if want := Display(ts, c.in); got.String != want {
				t.Errorf("SQL(title=%v, filename=%q) = %q, want %q", title, c.in, got.String, want)
			}
		}
	}
	// Both columns NULL stays NULL, so an outer COALESCE can go on.
	var got sql.NullString
	if err := db.QueryRow(`SELECT ` + SQL("NULL", "NULL")).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got.Valid {
		t.Errorf("SQL(NULL, NULL) = %q, want NULL", got.String)
	}
}

// TestSQLMatchesGoOnPostgres runs the same SQL text on a real Postgres
// (MAKTABA_TEST_PG_URL), so the handlers' one expression is proven on both
// backends a home can run.
func TestSQLMatchesGoOnPostgres(t *testing.T) {
	dsn := testpg.Get()
	if dsn == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset: skipping the real-Postgres check")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := `SELECT ` + SQL("v.title", "v.filename") + ` FROM (VALUES ($1::text, $2::text)) AS v(title, filename)`
	for _, c := range stripCases {
		for _, title := range []any{nil, "", " ", "Kept.mkv"} {
			var got sql.NullString
			if err := db.QueryRow(q, title, c.in).Scan(&got); err != nil {
				t.Fatal(err)
			}
			ts, _ := title.(string)
			if want := Display(ts, c.in); got.String != want {
				t.Errorf("SQL(title=%v, filename=%q) = %q, want %q", title, c.in, got.String, want)
			}
		}
	}
}
