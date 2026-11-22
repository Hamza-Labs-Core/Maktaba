package search

// Story 49.7's acceptance: the keyword leg answers in p95 < 50 ms on a 45,000-document
// library, for الله (in nine documents in ten), a sentence and a rare word. The fixture is
// a lecture library's shape: about 400 words a document drawn from a vocabulary of 17,576
// words by a Zipf law, الله in most documents, the sentence in some, the rare word in a
// few. It takes about a minute to build, so it is the replica job's
// (MAKTABA_TEST_SEARCH_LARGE=1, on its PostgreSQL 18).
//
// Story 49.8's acceptance on the same library: suggestions as you type answer in p95 < 50
// ms (the terms dictionary filled from the documents, as the keyword_index stage fills
// it), and so does did-you-mean.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchterms"
)

func TestBM25_Perf45k(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_SEARCH_LARGE") != "1" {
		t.Skip("MAKTABA_TEST_SEARCH_LARGE unset: the 45,000-document fixture is the replica job's")
	}
	db := bm25DB(t)
	const docs = 45000
	ctx := context.Background()
	t0 := time.Now()
	// the vocabulary: three letters of the alphabet's 26 from ب, then ون
	vocab := make([]string, 26*26*26)
	for i := range vocab {
		vocab[i] = string([]rune{rune(0x0628 + i%26), rune(0x0628 + (i/26)%26), rune(0x0628 + (i/676)%26)}) + "ون"
	}
	r := rand.New(rand.NewSource(49))
	zipf := rand.NewZipf(r, 1.07, 2, uint64(len(vocab)-1))
	body := func() string {
		var b strings.Builder
		for i := 0; i < 400; i++ {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(vocab[zipf.Uint64()])
		}
		if r.Float64() < 0.9 {
			b.WriteString(" قال الله تعالى والله أعلم")
		}
		if r.Float64() < 0.05 {
			b.WriteString(" إن الصبر عند المصيبة الأولى")
		} else if r.Float64() < 0.2 {
			b.WriteString(" الصبر " + vocab[zipf.Uint64()] + " عند " + vocab[zipf.Uint64()] + " المصيبة")
		}
		if r.Float64() < 0.002 {
			b.WriteString(" الاستغفار")
		}
		return b.String()
	}
	txn, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	vst, err := txn.Prepare(pq.CopyIn("videos", "id", "library_id", "content_hash", "path", "filename", "size_bytes", "mtime", "state"))
	if err != nil {
		t.Fatal(err)
	}
	id := func(i int) string { return fmt.Sprintf("49700000-0000-4000-8000-%012d", i) }
	for i := 0; i < docs; i++ {
		lib := libA
		if i%10 == 0 {
			lib = libB
		}
		if _, err := vst.Exec(id(i), lib, fmt.Sprintf("%064x", i+0x4970000), "/m/"+id(i), id(i)+".mp4", 1, time.Now(), "ready"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := vst.Exec(); err != nil {
		t.Fatal(err)
	}
	dst, err := txn.Prepare(pq.CopyIn("video_search_docs", "video_id", "library_id", "title", "speakers", "body", "transcript_id", "doc_version"))
	if err != nil {
		t.Fatal(err)
	}
	// the terms dictionary, as the keyword_index stage counts it: per library, each word's
	// documents
	type term struct{ lib, key string }
	df, shown := map[term]int{}, map[term]string{}
	for i := 0; i < docs; i++ {
		lib := libA
		if i%10 == 0 {
			lib = libB
		}
		title, b := "درس "+vocab[zipf.Uint64()], body()
		for k, s := range searchterms.DocTerms(title, "الشيخ", b) {
			df[term{lib, k}]++
			if _, ok := shown[term{lib, k}]; !ok {
				shown[term{lib, k}] = s
			}
		}
		if _, err := dst.Exec(id(i), lib, title, "الشيخ", b, id(i), 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dst.Exec(); err != nil {
		t.Fatal(err)
	}
	tst, err := txn.Prepare(pq.CopyIn("search_terms", "library_id", "term", "shown", "df"))
	if err != nil {
		t.Fatal(err)
	}
	for k, n := range df {
		if _, err := tst.Exec(k.lib, k.key, shown[k], n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tst.Exec(); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ANALYZE videos; ANALYZE video_search_docs; ANALYZE search_terms`); err != nil {
		t.Fatal(err)
	}
	buildIndexes(t, db)
	t.Logf("%d documents loaded and indexed in %s (%d dictionary rows)", docs, time.Since(t0).Round(time.Second), len(df))

	h := &Handler{DB: db}
	if ready, _ := h.bm25Now(ctx); !ready {
		t.Fatal("the indexes are not ready")
	}
	const runs = 40
	for _, c := range []struct {
		name, q string
		f       Filters
	}{
		{"الله", "الله", Filters{}},
		{"a sentence", "الصبر عند المصيبة", Filters{}},
		{"a rare word", "الاستغفار", Filters{}},
		{"الله, one library (a limited user)", "الله", Filters{LibraryID: []string{libB}}},
	} {
		var took []time.Duration
		var n int
		for i := 0; i < runs+3; i++ {
			s := time.Now()
			vs, err := h.keywordVideos(ctx, c.q, 500, c.f)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if i >= 3 { // the first runs warm the cache
				took = append(took, time.Since(s))
			}
			n = len(vs)
		}
		sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
		p50, p95 := took[len(took)/2], took[len(took)*95/100]
		t.Logf("%s: %d videos, p50 %s, p95 %s", c.name, n, p50.Round(time.Millisecond/10), p95.Round(time.Millisecond/10))
		if n == 0 {
			t.Errorf("%s: no video found", c.name)
		}
		if p95 > 50*time.Millisecond {
			t.Errorf("%s: p95 %s, want < 50ms", c.name, p95)
			q, args := bm25LegSQL(ctx, c.q, bm25TopK, c.f, "video_search_docs_bm25_text", "search_text", "maktaba_normalize")
			rows, err := db.Query("EXPLAIN (ANALYZE, BUFFERS) "+q, args...)
			if err == nil {
				var plan strings.Builder
				for rows.Next() {
					var l string
					_ = rows.Scan(&l)
					plan.WriteString(l + "\n")
				}
				_ = rows.Close()
				t.Logf("the exact leg's plan:\n%s", plan.String())
			}
		}
	}

	// story 49.8: suggestions and did-you-mean on the same library
	admin := &principal.Principal{UserID: userA, AccessAllLibraries: true}
	limitedUser := &principal.Principal{UserID: userB, Libraries: []string{libB}}
	misspelt := []rune(vocab[3])
	misspelt[1] = 'ك' // one letter off a frequent word (ك is in no word)
	for _, c := range []struct {
		name string
		run  func() (int, error)
	}{
		{"suggest ال (the commonest prefix)", func() (int, error) { s, err := h.suggest(ctx, admin, "ال"); return len(s), err }},
		{"suggest الص", func() (int, error) { s, err := h.suggest(ctx, admin, "الص"); return len(s), err }},
		{"suggest a sentence's last word", func() (int, error) { s, err := h.suggest(ctx, admin, "الصبر عند الم"); return len(s), err }},
		{"suggest ال, one library (a limited user)", func() (int, error) { s, err := h.suggest(ctx, limitedUser, "ال"); return len(s), err }},
		{"did-you-mean " + string(misspelt), func() (int, error) {
			d, err := h.didYouMean(ctx, string(misspelt), nil, 0)
			if d == "" {
				return 0, err
			}
			return 1, err
		}},
	} {
		var took []time.Duration
		var n int
		for i := 0; i < runs+3; i++ {
			s := time.Now()
			got, err := c.run()
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if i >= 3 {
				took = append(took, time.Since(s))
			}
			n = got
		}
		sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
		p50, p95 := took[len(took)/2], took[len(took)*95/100]
		t.Logf("%s: %d answers, p50 %s, p95 %s", c.name, n, p50.Round(time.Millisecond/10), p95.Round(time.Millisecond/10))
		if n == 0 {
			t.Errorf("%s: nothing answered", c.name)
		}
		if p95 > 50*time.Millisecond {
			t.Errorf("%s: p95 %s, want < 50ms", c.name, p95)
		}
	}
}
