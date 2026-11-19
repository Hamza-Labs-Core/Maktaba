package textnorm

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // the SQLite driver

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// corpus is text the parity test folds on both sides: every folded rune, mixed scripts, and
// text with nothing to fold.
var corpus = []string{
	"إسلام", "أحمد آمن ٱلله", "هدى مدرسة مؤمن سائل", "اللَّهُ أَكْبَرُ", "صبــر", "الرَّحْمٰنِ الرَّحِيمِ",
	"١٢٣٤٥٦٧٨٩٠ ۰۱۲۳۴۵۶۷۸۹", "café crème déjà vu", "Ayah 255: آية الكرسي", "",
	"ًٌٍَُِّْٰـ", "plain ascii 0123", "قرآن – ﷲ ﻻ", // presentation forms are left as they are, on both sides
}

// Epic 49 story 49.4: Go's Normalize and Postgres's maktaba_normalize() (migration 0231)
// fold the same text the same way, so SQLite and Postgres installs agree.
func TestNormalizeMatchesPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	for _, s := range corpus {
		var pg string
		if err := db.QueryRow(`SELECT maktaba_normalize($1)`, s).Scan(&pg); err != nil {
			t.Fatalf("maktaba_normalize(%q): %v", s, err)
		}
		if got := Normalize(s); got != pg {
			t.Errorf("%q: Go %q, Postgres %q", s, got, pg)
		}
	}
}

// The acceptance on Postgres: the spelling variants find each other (exact configuration),
// and word forms find each other (maktaba_stem) with the word as typed ranked first.
func TestArabicSearchOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	match := func(cfg, text, q string) bool {
		t.Helper()
		var ok bool
		tsv, qry := "maktaba_tsv", "maktaba_query"
		if cfg == "stem" {
			tsv, qry = "maktaba_tsv_stem", "maktaba_query_stem"
		}
		if err := db.QueryRow(`SELECT `+tsv+`($1) @@ `+qry+`($2)`, text, q).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	for _, p := range [][2]string{{"إسلام", "اسلام"}, {"هدى", "هدي"}, {"مدرسة", "مدرسه"}, {"اللَّهُ", "الله"}} {
		if !match("exact", "قال: "+p[0]+" هنا", p[1]) {
			t.Errorf("%q does not find %q", p[1], p[0])
		}
	}
	if match("exact", "الصبر", "صبروا") {
		t.Error("the exact configuration matches another form of the word")
	}
	for _, q := range []string{"يصبر", "صبروا", "تصبر"} {
		if !match("stem", "الصبر عند المصيبة", q) {
			t.Errorf("%q does not find الصبر", q)
		}
	}
	for _, w := range []string{"يوم", "تمر", "نور"} { // a three-letter word keeps its first letter
		if match("stem", w, w[len("ي"):]) {
			t.Errorf("%q lost its first letter", w)
		}
	}
	// the word as typed first: the exact score weighs more than the stemmed one
	rows, err := db.Query(`
		SELECT body FROM (VALUES ('الصبر مفتاح الفرج'), ('هو يصبر كثيرا'), ('صبروا وصابروا'), ('قصة اخرى')) d(body)
		WHERE maktaba_tsv(body) @@ maktaba_query($1) OR maktaba_tsv_stem(body) @@ maktaba_query_stem($1)
		ORDER BY 2 * ts_rank(maktaba_tsv(body), maktaba_query($1)) + ts_rank(maktaba_tsv_stem(body), maktaba_query_stem($1)) DESC,
		         body`, "يصبر")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var b string
		_ = rows.Scan(&b)
		got = append(got, b)
	}
	if len(got) != 3 || got[0] != "هو يصبر كثيرا" {
		t.Errorf("يصبر ranks %q, want the exact word first and the two other forms after it", got)
	}
}

// The acceptance on SQLite: FTS5 over the normalised text finds every spelling; without the
// normalisation it does not (the control: remove_diacritics leaves Arabic alone).
func TestArabicSearchOnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE lines USING fts5(text, tokenize='unicode61 remove_diacritics 2')`); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"قال: إسلام هنا", "هدى للمتقين", "في المدرسة", "اللَّهُ أكبر"} {
		if _, err := db.Exec(`INSERT INTO lines (text) VALUES (?)`, Normalize(s)); err != nil {
			t.Fatal(err)
		}
	}
	found := func(q string) bool {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM lines WHERE lines MATCH ?`, `"`+strings.ReplaceAll(q, `"`, `""`)+`"`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	for _, q := range []string{"اسلام", "هدي", "المدرسه", "الله"} {
		if !found(Normalize(q)) {
			t.Errorf("%q finds nothing on SQLite", q)
		}
	}
	if found("أكبر") {
		t.Error("an unnormalised query matched the normalised text: the control does not hold")
	}
}
