package search

// Story 49.9: keyword search on documents as long as the home's. The home's transcripts
// average some 5,000 words a video (23.1 M lines over 45,000 videos); story 49.7's 45,000-document
// fixture has 400. A BM25 query that scored each returned row's whole text again took 180 ms
// on 5,000 documents of 5,000 words (1.8 ms a row), and a test on short documents could not
// see it. Here: 5,000 documents of 5,000 words, and the p95 of الله and of a sentence under
// 50 ms. About a minute to build: the replica job's (MAKTABA_TEST_SEARCH_LARGE=1).

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
)

func TestBM25_LongDocuments(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_SEARCH_LARGE") != "1" {
		t.Skip("MAKTABA_TEST_SEARCH_LARGE unset: the long-document fixture is the replica job's")
	}
	db := bm25DB(t)
	const docs, words = 5000, 5000
	vocab := make([]string, 2*26*26*26)
	for i := range vocab {
		vocab[i] = string([]rune{rune(0x0628 + i%26), rune(0x0628 + (i/26)%26), rune(0x0628 + (i/676)%26)}) + []string{"ات", "ون"}[i/17576]
	}
	r := rand.New(rand.NewSource(4979))
	zipf := rand.NewZipf(r, 1.1, 3, uint64(len(vocab)-1))
	id := func(i int) string { return fmt.Sprintf("49790000-0000-4000-8000-%012d", i) }
	t0 := time.Now()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	vst, err := tx.Prepare(pq.CopyIn("videos", "id", "library_id", "content_hash", "path", "filename", "size_bytes", "mtime", "state"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < docs; i++ {
		if _, err := vst.Exec(id(i), libA, fmt.Sprintf("%064x", i+0x4979000), "/m/"+id(i), id(i)+".mp4", 1, time.Now(), "ready"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := vst.Exec(); err != nil {
		t.Fatal(err)
	}
	dst, err := tx.Prepare(pq.CopyIn("video_search_docs", "video_id", "library_id", "title", "speakers", "body", "transcript_id", "doc_version"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < docs; i++ {
		var b strings.Builder
		for k := 0; k < words; k++ {
			if k > 0 {
				b.WriteByte(" \n"[min(k%10, 1)^1])
			}
			switch {
			case r.Intn(60) == 0:
				b.WriteString("الله")
			case r.Intn(2000) == 0:
				b.WriteString("الصبر عند المصيبة")
			default:
				b.WriteString(vocab[zipf.Uint64()])
			}
		}
		if _, err := dst.Exec(id(i), libA, "درس", "", b.String(), id(i), 2); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dst.Exec(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ANALYZE videos; ANALYZE video_search_docs`); err != nil {
		t.Fatal(err)
	}
	buildIndexes(t, db)
	t.Logf("%d documents of %d words loaded and indexed in %s", docs, words, time.Since(t0).Round(time.Second))

	h := &Handler{DB: db}
	ctx := context.Background()
	for _, q := range []string{"الله", "الصبر عند المصيبة"} {
		var took []time.Duration
		n := 0
		for i := 0; i < 23; i++ {
			s := time.Now()
			vs, err := h.keywordVideos(ctx, q, 500, Filters{})
			if err != nil {
				t.Fatal(err)
			}
			if i >= 3 {
				took = append(took, time.Since(s))
			}
			n = len(vs)
		}
		sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
		p95 := took[len(took)*95/100]
		t.Logf("%q: %d videos, p50 %s, p95 %s", q, n, took[len(took)/2].Round(time.Millisecond/10), p95.Round(time.Millisecond/10))
		if n == 0 || p95 > 50*time.Millisecond {
			t.Errorf("%q: %d videos, p95 %s: want some, under 50 ms", q, n, p95)
		}
	}
}
