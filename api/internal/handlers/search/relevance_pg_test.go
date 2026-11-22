package search

// Story 49.9's regression test on a synthetic library (the replica job, PostgreSQL 18): the
// relevance check's generator, with its fixed seed, draws the same searches from the same
// library, and every one passes. The library has a lecture library's shape: a Zipf
// vocabulary with real Arabic words (hamza forms, ta marbuta, alef maqsura) in it, الله in
// most videos at very different rates per minute, videos of 5 to 120 minutes, chapters whose
// titles are taken from their own lines (as inferred chapters are), and topics whose videos
// say the topic's word most. A home runs the same check on its own library with
// `maktaba-api search-relevance`.

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// relevanceWords are real words among the synthetic vocabulary: the spelling variants need
// letters that fold.
var relevanceWords = strings.Fields(`إسلام مدرسة أحمد الصلاة الزكاة هدى مسألة سؤال قراءة آية المؤمن الأنبياء رحمة
	ذكرى موسى عيسى الجنة الآخرة أمانة إيمان مؤمنة الدعاء القرآن الصدقة أخلاق رؤية إمام`)

func TestBM25_Relevance(t *testing.T) {
	db := bm25DB(t)
	ctx := context.Background()
	const videos = 3000
	r := rand.New(rand.NewSource(499))
	// a transcript library's vocabulary is large (most words rare): three letters and one of
	// two endings, 35,152 words
	vocab := make([]string, 2*26*26*26)
	for i := range vocab {
		vocab[i] = string([]rune{rune(0x0628 + i%26), rune(0x0628 + (i/26)%26), rune(0x0628 + (i/676)%26)}) + []string{"ات", "ون"}[i/17576]
	}
	for i, w := range relevanceWords {
		vocab[50+i*37] = w // spread through the frequencies
	}
	zipf := rand.NewZipf(r, 1.1, 3, uint64(len(vocab)-1))
	id := func(i int) string { return fmt.Sprintf("49900000-0000-4000-8000-%012d", i) }

	type video struct {
		lines []string
		dur   float64
	}
	vs := make([]video, videos)
	for i := range vs {
		minutes := 5 + r.Intn(116)
		allah := r.Intn(4) // uses per 10 minutes: 0 to 3, then a few dense lectures
		if r.Intn(10) == 0 {
			allah = 10 + r.Intn(20)
		}
		v := video{dur: float64(minutes * 60)}
		for l := 0; l < minutes; l++ {
			words := make([]string, 10)
			for k := range words {
				words[k] = vocab[zipf.Uint64()]
			}
			if r.Intn(10) < allah {
				words[r.Intn(10)] = "الله"
			}
			if r.Intn(40) == 0 {
				words[r.Intn(10)] = "الصبر"
			}
			v.lines = append(v.lines, strings.Join(words, " "))
		}
		vs[i] = v
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	copyIn := func(table string, cols []string, rows func(add func(...any))) {
		t.Helper()
		st, err := tx.Prepare(pq.CopyIn(table, cols...))
		if err != nil {
			t.Fatal(err)
		}
		rows(func(vals ...any) {
			if _, err := st.Exec(vals...); err != nil {
				t.Fatal(err)
			}
		})
		if _, err := st.Exec(); err != nil {
			t.Fatal(err)
		}
	}
	copyIn("videos", []string{"id", "library_id", "content_hash", "path", "filename", "size_bytes", "mtime", "state", "title", "duration_sec"}, func(add func(...any)) {
		for i, v := range vs {
			add(id(i), libA, fmt.Sprintf("%064x", i+0x4990000), "/m/"+id(i), id(i)+".mp4", 1, time.Now(), "ready", fmt.Sprintf("درس %d", i), v.dur)
		}
	})
	copyIn("audio_tracks", []string{"id", "video_id", "track_index"}, func(add func(...any)) {
		for i := range vs {
			add(4990000+i, id(i), 0)
		}
	})
	copyIn("transcripts", []string{"id", "video_id", "audio_track_id", "language", "backend", "model", "is_active"}, func(add func(...any)) {
		for i := range vs {
			add(id(i), id(i), 4990000+i, "ar", "test", "m", true)
		}
	})
	copyIn("transcript_segments", []string{"transcript_id", "seq", "start_sec", "end_sec", "text"}, func(add func(...any)) {
		for i, v := range vs {
			for l, text := range v.lines {
				add(id(i), l, float64(l*60), float64(l*60+59), text)
			}
		}
	})
	// chapters: two lines' first words of 300 videos, as an inferred chapter's title
	copyIn("chapters", []string{"video_id", "seq", "start_sec", "title", "source"}, func(add func(...any)) {
		for i := 0; i < 300; i++ {
			v := vs[i*7]
			for c := 0; c < 2; c++ {
				line := strings.Fields(v.lines[r.Intn(len(v.lines))])
				add(id(i*7), c, float64(c*60), strings.Join(line[:4], " "), "inferred")
			}
		}
	})
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// topics: 20 mid-frequency words, each with the 20 videos that say it most
	counts := func(w string) []int {
		n := make([]int, videos)
		for i, v := range vs {
			for _, l := range v.lines {
				n[i] += strings.Count(" "+l+" ", " "+w+" ")
			}
		}
		return n
	}
	for k := 0; k < 20; k++ {
		w := vocab[200+k*13]
		n := counts(w)
		order := make([]int, videos)
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool { return n[order[a]] > n[order[b]] })
		if _, err := db.Exec(`INSERT INTO library_topics (library_id, topic_id, label, centroid_vec, video_count) VALUES ($1, $2, $3, '\x00', 20)`,
			libA, k, w+" "+vocab[3000+k]); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 20; j++ {
			if _, err := db.Exec(`INSERT INTO video_topics (video_id, library_id, topic_id, score) VALUES ($1, $2, $3, $4)`,
				id(order[j]), libA, k, float64(20-j)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`INSERT INTO video_search_docs (video_id, library_id, title, body, transcript_id, doc_version)
		SELECT v.id, v.library_id, coalesce(v.title, ''), string_agg(s.text, E'\n' ORDER BY s.seq), t.id, 2
		  FROM videos v JOIN transcripts t ON t.video_id = v.id AND t.is_active
		  JOIN transcript_segments s ON s.transcript_id = t.id
		 WHERE v.library_id = $1 GROUP BY v.id, t.id`, libA); err != nil {
		t.Fatal(err)
	}
	pgTerms(t, db)
	if _, err := db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	buildIndexes(t, db)

	o := DefaultRelevance
	o.Runs = 10
	cases, err := GenerateRelevance(ctx, db, o)
	if err != nil {
		t.Fatal(err)
	}
	again, err := GenerateRelevance(ctx, db, o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cases, again) {
		t.Fatal("the same seed drew other searches")
	}
	kinds := map[string]int{}
	variants := map[string]int{}
	for _, c := range cases {
		kinds[c.Kind]++
		if c.Kind == RelKnownItem {
			variants[c.Variant]++
		}
	}
	t.Logf("%d searches: %v, known-item variants %v", len(cases), kinds, variants)
	if variants["as-written"] < 150 || variants["spelling"] == 0 || variants["word-form"] == 0 ||
		kinds[RelChapter] < 30 || kinds[RelTopic] < 15 || kinds[RelCommon] < 10 {
		t.Fatalf("the generator drew too few searches: %v %v", kinds, variants)
	}
	rep, err := (&Handler{DB: db}).RunRelevance(ctx, cases, o.Runs)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{RelKnownItem, RelChapter, RelTopic, RelCommon} {
		t.Logf("%s: %d of %d passed", k, rep.Passed[k], rep.Total[k])
	}
	// each failure with where its video stands in each index, and whether its phrase is
	// found in its lines and in its document: what to tune
	hh := &Handler{DB: db}
	for i, f := range rep.Failed() {
		t.Errorf("%s %s %q: rank %d (want top %d) %s %s", f.Kind, f.Variant, f.Query, f.Rank, f.Top, f.P95, f.Note)
		if i < 20 && f.Kind == RelKnownItem {
			pos := func(vs []savedVideo) (int, float64) {
				for j, v := range vs {
					if canonID(v.V) == canonID(f.Want[0]) {
						return j + 1, v.S
					}
				}
				return 0, 0
			}
			ex, _ := hh.bm25Leg(ctx, f.Query, 100, Filters{}, "video_search_docs_bm25_text", "search_text", "maktaba_normalize")
			st, _ := hh.bm25Leg(ctx, f.Query, 50, Filters{}, "video_search_docs_bm25_stem_text", "stem_text", "maktaba_stem_prepare")
			var ph int
			_ = db.QueryRow(`SELECT count(*) FROM transcript_segments s JOIN transcripts t ON t.id = s.transcript_id WHERE t.video_id = $1 AND s.search_tsv @@ phraseto_tsquery('maktaba_search', $2)`, f.Want[0], f.Query).Scan(&ph)
			var norm, stemq string
			_ = db.QueryRow(`SELECT maktaba_normalize($1), maktaba_stem_prepare($1)`, f.Query).Scan(&norm, &stemq)
			er, es := pos(ex)
			sr, ss := pos(st)
			var hit bool
			_ = db.QueryRow(`SELECT lower(search_text) ~ $2 FROM video_search_docs WHERE video_id = $1`, f.Want[0], phrasePattern(f.Query)).Scan(&hit)
			t.Logf("  exact rank %d (%.2f; first %.2f), stem rank %d (%.2f; first %.2f), phrase: %d lines, document %v; normalised %q, stem %q",
				er, es, first(ex), sr, ss, first(st), ph, hit, norm, stemq)
		}
	}
	for _, x := range rep.Results {
		if x.Kind == RelCommon {
			t.Logf("common %q: p95 %s; %s", x.Query, x.P95.Round(time.Millisecond/10), x.Note)
		}
	}
}

func first(vs []savedVideo) float64 {
	if len(vs) == 0 {
		return 0
	}
	return vs[0].S
}
