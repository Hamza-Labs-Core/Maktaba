package searchindex

// Slot 0232 (Epic 49, story 49.5): video_search_docs goes up and down on Postgres and on
// its SQLite twin, the manifest lists it, and a machine's copy leaves it out. With
// pg_textsearch (PostgreSQL 18, preloaded: the replica job's server) Ensure makes its two
// BM25 indexes, one on maktaba_search and one on maktaba_stem, and a query ranks through them.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/textnorm"
)

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the migrations")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "db", "migrations")
}

// gooseStatements returns a migration file's Up and Down statements.
func gooseStatements(t *testing.T, name string) (up, down []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), name))
	if err != nil {
		t.Fatal(err)
	}
	var cur *[]string
	var stmt []string
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		switch strings.TrimSpace(line) {
		case "-- +goose Up":
			cur = &up
		case "-- +goose Down":
			cur = &down
		case "-- +goose StatementBegin":
			in, stmt = true, nil
		case "-- +goose StatementEnd":
			if cur == nil {
				t.Fatalf("%s: a statement before Up or Down", name)
			}
			*cur = append(*cur, strings.Join(stmt, "\n"))
			in = false
		default:
			if in {
				stmt = append(stmt, line)
			}
		}
	}
	if len(up) == 0 || len(down) == 0 {
		t.Fatalf("%s: %d up and %d down statements", name, len(up), len(down))
	}
	return up, down
}

func run(t *testing.T, db *sql.DB, what string, stmts []string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v\n%s", what, err, s)
		}
	}
}

func scalar[T any](t *testing.T, db *sql.DB, q string, args ...any) T {
	t.Helper()
	var v T
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

// textsearchHere reports whether this server can hold pg_textsearch (it has it and
// preloads it), and makes it in db when it can, as the bundled server's bootstrap does: in
// the schema textsearch, so its operator and functions are named with it.
func textsearchHere(t *testing.T, db *sql.DB) bool {
	t.Helper()
	if !scalar[bool](t, db, `SELECT EXISTS (SELECT FROM pg_available_extensions WHERE name = 'pg_textsearch')
		AND current_setting('shared_preload_libraries') ~ '(^|[\s,])pg_textsearch([\s,]|$)'`) {
		return false
	}
	// in its own schema, as the bundled server makes it (pgbundle.textsearchSQL)
	for _, q := range []string{`CREATE SCHEMA IF NOT EXISTS textsearch`,
		`CREATE EXTENSION IF NOT EXISTS pg_textsearch SCHEMA textsearch`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return true
}

func TestSlot0232UpAndDownOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t) // migrated to the head: 0232 is up
	up, down := gooseStatements(t, "0232_video_search_docs.sql")
	objects := func() int {
		return scalar[int](t, db, `SELECT (SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'video_search_docs')
			+ (SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'video_search_docs_library_idx')
			+ (SELECT count(*) FROM pg_proc WHERE proname = 'maktaba_search_index_statements')`)
	}
	excluded := func() bool {
		return scalar[bool](t, db, `SELECT 'video_search_docs' = ANY(cluster_copy_excluded())`)
	}
	if objects() != 3 || !excluded() {
		t.Fatalf("after the migrations: %d of the table, its library index and the function; excluded %v", objects(), excluded())
	}
	if scalar[bool](t, db, `SELECT EXISTS (SELECT 1 FROM cluster_copy_tables() c WHERE c::text = 'video_search_docs')`) {
		t.Fatal("video_search_docs is in a machine's copy")
	}
	cols := scalar[string](t, db, `SELECT string_agg(column_name || ' ' || data_type || ' ' || is_nullable, ', ' ORDER BY ordinal_position)
		FROM information_schema.columns WHERE table_name = 'video_search_docs'`)
	if want := "video_id uuid NO, library_id uuid NO, language text YES, title text NO, speakers text NO, body text NO, " +
		"transcript_id uuid NO, built_at timestamp with time zone NO"; !strings.HasPrefix(cols, want) { // later slots add theirs
		t.Fatalf("columns:\n%s\nwant\n%s", cols, want)
	}
	if !textsearchHere(t, db) {
		// PostgreSQL 16 or no preload: the table has no BM25 index, and Ensure makes none
		if ran, err := Ensure(context.Background(), db); err != nil || len(ran) != 0 {
			t.Fatalf("Ensure without pg_textsearch ran %q (%v)", ran, err)
		}
		if n := scalar[int](t, db, `SELECT count(*) FROM pg_indexes WHERE indexname LIKE 'video_search_docs_bm25%'`); n != 0 {
			t.Fatalf("%d BM25 indexes without pg_textsearch", n)
		}
	}
	// from 0232's own list: a later slot's is longer (slot 0235 adds search_terms)
	run(t, db, "0232's list", up)
	before := scalar[int](t, db, `SELECT cardinality(cluster_copy_excluded())`)
	run(t, db, "down", down)
	if objects() != 0 || excluded() {
		t.Fatalf("after down: %d objects left; excluded %v", objects(), excluded())
	}
	// Down restores slot 0230's list: one name fewer, the broadcast secrets still out
	if n := scalar[int](t, db, `SELECT cardinality(cluster_copy_excluded())`); n != before-1 {
		t.Errorf("after down the exclusion has %d names, want %d", n, before-1)
	}
	if !scalar[bool](t, db, `SELECT 'broadcast_ingest_tokens' = ANY(cluster_copy_excluded())`) {
		t.Error("slot 0230's exclusion lost broadcast_ingest_tokens after down")
	}
	run(t, db, "up", up)
	if objects() != 3 || !excluded() {
		t.Fatalf("after up again: %d objects; excluded %v", objects(), excluded())
	}
	run(t, db, "up twice", up) // every statement can run again
}

// The acceptance on PostgreSQL 18 (story 49.5, the indexes as slot 0234 builds them): one
// BM25 index on maktaba_search (the text as typed, normalised), one on maktaba_stem (its
// word forms), both over title, speakers and body (the columns search_text and stem_text);
// a ranked query on each is answered by its index. Skipped where pg_textsearch is not (16).
func TestSlot0232BM25Indexes(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	if !textsearchHere(t, db) {
		t.Skip("no pg_textsearch on this server (PostgreSQL 16, or not preloaded): the replica job runs this on 18")
	}
	ctx := context.Background()
	ran, err := Ensure(ctx, db)
	if err != nil || len(ran) != 2 {
		t.Fatalf("Ensure ran %q (%v), want the two indexes", ran, err)
	}
	for _, s := range ran {
		if !strings.HasPrefix(s, "CREATE INDEX CONCURRENTLY IF NOT EXISTS ") {
			t.Errorf("not a concurrent build: %s", s)
		}
	}
	if ran, err := Ensure(ctx, db); err != nil || len(ran) != 0 {
		t.Fatalf("a second Ensure ran %q (%v)", ran, err)
	}
	// a build that did not finish (a concurrent build stopped part way leaves its index
	// invalid): the next Ensure drops it and builds it again
	if _, err := db.Exec(`UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'video_search_docs_bm25_stem_text'::regclass`); err != nil {
		t.Fatal(err)
	}
	ran, err = Ensure(ctx, db)
	if err != nil || len(ran) != 2 || !strings.HasPrefix(ran[0], "DROP INDEX CONCURRENTLY IF EXISTS public.video_search_docs_bm25_stem_text") ||
		!strings.Contains(ran[1], "video_search_docs_bm25_stem_text") {
		t.Fatalf("Ensure after an invalid build ran %q (%v)", ran, err)
	}
	if !scalar[bool](t, db, `SELECT indisvalid FROM pg_index WHERE indexrelid = 'video_search_docs_bm25_stem_text'::regclass`) {
		t.Fatal("the stemmed index is still invalid")
	}
	for name, want := range map[string][2]string{"video_search_docs_bm25_text": {"search_text", "maktaba_search"},
		"video_search_docs_bm25_stem_text": {"stem_text", "maktaba_stem"}} {
		def := scalar[string](t, db, `SELECT pg_get_indexdef($1::regclass)`, name)
		if !strings.Contains(def, "USING bm25 ("+want[0]+")") || !strings.Contains(def, "public."+want[1]) {
			t.Errorf("%s: %s", name, def)
		}
	}
	lib := scalar[string](t, db, `INSERT INTO libraries (name, roots) VALUES ('m0232', ARRAY['/tmp/m0232']) RETURNING id::text`)
	docs := []struct{ title, speakers, body string }{
		{"درس في الصبر", "الشيخ أحمد", "إن الصبر عند المصيبة الأولى، والصابرون لهم أجر"},
		{"Patience", "Ahmad", "talking about patience and gratitude"},
	}
	for i := range 40 {
		docs = append(docs, struct{ title, speakers, body string }{fmt.Sprintf("درس %d", i), "", fmt.Sprintf("كلمة %d عن الطهارة", i)})
	}
	for i, d := range docs {
		var vid string
		if err := db.QueryRow(`INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime)
			VALUES ($1, $2, $3, $3, 1, now()) RETURNING id::text`, lib, fmt.Sprintf("%064x", 0x2320000+i), fmt.Sprintf("/tmp/m0232/%d.mp4", i)).Scan(&vid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO video_search_docs (video_id, library_id, title, speakers, body, transcript_id)
			VALUES ($1, $2, $3, $4, $5, gen_random_uuid())`, vid, lib, d.title, d.speakers, d.body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	top := func(index, fn, q string) string {
		t.Helper()
		col := "search_text"
		if strings.HasSuffix(index, "_stem_text") {
			col = "stem_text"
		}
		query := `SELECT title FROM video_search_docs
			ORDER BY ` + col + ` OPERATOR(textsearch.<@>) textsearch.to_bm25query(public.` + fn + `($1), '` + index + `') LIMIT 1`
		var plan strings.Builder
		rows, err := db.Query(`EXPLAIN (COSTS OFF) `+query, q)
		if err != nil {
			t.Fatalf("%s: %v", index, err)
		}
		for rows.Next() {
			var l string
			_ = rows.Scan(&l)
			plan.WriteString(l + "\n")
		}
		_ = rows.Close()
		if !strings.Contains(plan.String(), "Index Scan using "+index) {
			t.Errorf("%q is not answered by %s:\n%s", q, index, plan.String())
		}
		return scalar[string](t, db, query, q)
	}
	// spelling variants (أ/ا, ة/ه) find the document through the exact index
	if got := top("video_search_docs_bm25_text", "maktaba_normalize", "احمد المصيبه"); got != "درس في الصبر" {
		t.Errorf("the exact index ranks %q first", got)
	}
	// another form of the word (يصبر, الصابرون's root) finds it through the stemmed index
	if got := top("video_search_docs_bm25_stem_text", "maktaba_stem_prepare", "يصبر"); got != "درس في الصبر" {
		t.Errorf("the stemmed index ranks %q first", got)
	}
	if got := top("video_search_docs_bm25_stem_text", "maktaba_stem_prepare", "talks"); got != "Patience" {
		t.Errorf("the stemmed index ranks %q first for English", got)
	}
	// a video deleted takes its document
	if _, err := db.Exec(`DELETE FROM videos WHERE library_id = $1`, lib); err != nil {
		t.Fatal(err)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM video_search_docs`); n != 0 {
		t.Errorf("%d documents left after their videos went", n)
	}
	// Down drops the indexes with the table
	_, down := gooseStatements(t, "0232_video_search_docs.sql")
	run(t, db, "down", down)
	if n := scalar[int](t, db, `SELECT count(*) FROM pg_class WHERE relname LIKE 'video_search_docs%'`); n != 0 {
		t.Errorf("%d of the slot's relations left after down", n)
	}
}

func TestSlot0232OnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "s.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	run(t, db, "videos", []string{
		`CREATE TABLE libraries (id TEXT PRIMARY KEY)`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE)`,
		`INSERT INTO libraries VALUES ('l')`,
		`INSERT INTO videos VALUES ('v1', 'l'), ('v2', 'l')`,
	})
	up, down := gooseStatements(t, "0232_video_search_docs.sqlite.sql")
	run(t, db, "up", up)
	add := func(vid, title, body string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO video_search_docs (video_id, library_id, title, body, transcript_id) VALUES (?, 'l', ?, ?, 't')`, vid, title, body); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO video_search_docs_fts (video_id, title, speakers, body) VALUES (?, ?, '', ?)`,
			vid, textnorm.Normalize(title), textnorm.Normalize(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("v1", "درس في الصبر", "إن الصبر عند المصيبة")
	add("v2", "الطهارة", "كلمة عن الوضوء")
	var got string
	if err := db.QueryRow(`SELECT d.video_id FROM video_search_docs_fts f JOIN video_search_docs d ON d.video_id = f.video_id
		WHERE video_search_docs_fts MATCH ? ORDER BY bm25(video_search_docs_fts) LIMIT 1`, textnorm.Normalize("المصيبه")).Scan(&got); err != nil || got != "v1" {
		t.Fatalf("FTS5 finds %q (%v), want v1", got, err)
	}
	// a video deleted takes its document and its FTS5 row
	if _, err := db.Exec(`DELETE FROM videos WHERE id = 'v1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM video_search_docs) + (SELECT count(*) FROM video_search_docs_fts)`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("after the video went: %d rows (%v), want v2's 2", n, err)
	}
	run(t, db, "down", down)
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'video_search_docs%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after down: %d of the slot's objects left (%v)", n, err)
	}
	run(t, db, "up again", up)
}

func TestManifestListsSlot0232(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `0232` | `0232_video_search_docs.sql` |") {
		t.Fatal("MANIFEST.md has no row for slot 0232")
	}
	for _, f := range []string{"0232_video_search_docs.sql", "0232_video_search_docs.sqlite.sql"} {
		if _, err := os.Stat(filepath.Join(migrationsDir(t), f)); err != nil {
			t.Fatal(err)
		}
	}
}

// A database migrated by an older release (no slot 0232) needs no index: Ensure runs nothing.
func TestEnsureBeforeSlot0232(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	_, down := gooseStatements(t, "0232_video_search_docs.sql")
	run(t, db, "down", down)
	if ran, err := Ensure(context.Background(), db); err != nil || ran != nil {
		t.Fatalf("Ensure before slot 0232 ran %q (%v)", ran, err)
	}
}

// Story 49.6: one builder at a time. While another API holds the builders' lock, BuildOnce
// builds nothing and says so; once it is free, it builds.
func TestBuildOnceOneAtATime(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	if !textsearchHere(t, db) {
		t.Skip("no pg_textsearch on this server (PostgreSQL 16, or not preloaded): the replica job runs this on 18")
	}
	ctx := context.Background()
	other, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		t.Fatal(err)
	}
	if ran, locked, err := BuildOnce(ctx, db); err != nil || locked || ran != nil {
		t.Fatalf("BuildOnce beside another builder: ran %q, locked %v (%v)", ran, locked, err)
	}
	if ready, _ := Ready(ctx, db); ready {
		t.Fatal("Ready with no index built")
	}
	if _, err := other.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lockKey); err != nil {
		t.Fatal(err)
	}
	if ran, locked, err := BuildOnce(ctx, db); err != nil || !locked || len(ran) != 2 {
		t.Fatalf("BuildOnce alone: ran %q, locked %v (%v)", ran, locked, err)
	}
	if ready, err := Ready(ctx, db); err != nil || !ready {
		t.Fatalf("Ready after the build = %v (%v)", ready, err)
	}
	// the session's own timeouts are back as they were on the pooled connection
	var st string
	if err := db.QueryRow(`SHOW statement_timeout`).Scan(&st); err != nil || st != "0" {
		t.Fatalf("statement_timeout after BuildOnce = %q (%v)", st, err)
	}
}
