package search

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
)

type fakeSearcher struct {
	hits  []grpcpipeline.SemanticHit
	err   error
	calls int
	libs  []string
	k     int
}

func (f *fakeSearcher) SemanticSearch(_ context.Context, _ string, k int, libs, _ []string) ([]grpcpipeline.SemanticHit, error) {
	f.calls++
	f.k, f.libs = k, libs
	return f.hits, f.err
}

func semanticDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "sem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, detected_language TEXT, deleted_at TEXT)`,
		`CREATE TABLE transcripts (id TEXT PRIMARY KEY, video_id TEXT, is_active INTEGER)`,
		`CREATE TABLE transcript_segments (id INTEGER PRIMARY KEY, transcript_id TEXT,
			start_sec REAL, end_sec REAL, text TEXT)`,
		`INSERT INTO videos VALUES ('v1','lib-a','ar',NULL), ('v2','lib-b','ar',NULL),
			('v3','lib-a','ar','2026-01-01')`,
		`INSERT INTO transcripts VALUES ('t1','v1',1), ('t0','v1',0), ('t2','v2',1), ('t3','v3',1)`,
		`INSERT INTO transcript_segments VALUES
			(1,'t1',0,4,'صلاة الفجر'), (2,'t1',4,8,'زكاة المال'), (3,'t0',0,4,'قديم'),
			(4,'t2',10,12,'صوم رمضان'), (5,'t3',0,1,'محذوف')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return db
}

func TestGRPCSemantic_LoadsSnippetsInRankOrderAndDropsStaleHits(t *testing.T) {
	db := semanticDB(t)
	be := &fakeSearcher{hits: []grpcpipeline.SemanticHit{
		{SegmentID: 2, VideoID: "v1", Score: 0.9, Text: "stale text from the index"},
		{SegmentID: 4, VideoID: "v2", Score: 0.8},
		{SegmentID: 3, VideoID: "v1", Score: 0.7},  // inactive transcript
		{SegmentID: 5, VideoID: "v3", Score: 0.6},  // deleted video
		{SegmentID: 99, VideoID: "v1", Score: 0.5}, // segment gone
		{SegmentID: 1, VideoID: "v1", Score: 0.4},
	}}
	g := NewGRPCSemantic(be, db)
	hits, err := g.Search(context.Background(), "صلاة", 25, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Hit{
		{SegmentID: 2, VideoID: "v1", StartSec: 4, EndSec: 8, Snippet: "زكاة المال", Score: 0.9},
		{SegmentID: 4, VideoID: "v2", StartSec: 10, EndSec: 12, Snippet: "صوم رمضان", Score: 0.8},
		{SegmentID: 1, VideoID: "v1", StartSec: 0, EndSec: 4, Snippet: "صلاة الفجر", Score: 0.4},
	}
	if len(hits) != len(want) {
		t.Fatalf("hits=%+v", hits)
	}
	for i := range want {
		if hits[i] != want[i] {
			t.Errorf("hit %d = %+v want %+v", i, hits[i], want[i])
		}
	}
	if be.k != 25 {
		t.Errorf("k=%d", be.k)
	}

	// library + language filters go to the backend and into SQL
	scoped, err := g.Search(context.Background(), "x", 5, Filters{LibraryID: []string{"lib-a"}, Language: []string{"ar"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(be.libs) != 1 || be.libs[0] != "lib-a" {
		t.Errorf("libs=%v", be.libs)
	}
	for _, h := range scoped {
		if h.VideoID != "v1" {
			t.Errorf("hit outside lib-a: %+v", h)
		}
	}
	if len(scoped) != 2 {
		t.Errorf("scoped=%+v", scoped)
	}
	none, err := g.Search(context.Background(), "x", 5, Filters{Language: []string{"en"}})
	if err != nil || len(none) != 0 {
		t.Errorf("language filter: %+v %v", none, err)
	}
}

func TestGRPCSemantic_ErrorsAndNotConfigured(t *testing.T) {
	be := &fakeSearcher{err: errors.New("pipeline: rpc error: unavailable")}
	g := NewGRPCSemantic(be, nil)
	if _, err := g.Search(context.Background(), "x", 5, Filters{}); err == nil {
		t.Fatal("a backend error must surface so the handler degrades")
	}

	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	be = &fakeSearcher{err: errors.New("pipeline: semantic search backend not configured")}
	g = NewGRPCSemantic(be, nil)
	g.NowFunc = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		hits, err := g.Search(context.Background(), "x", 5, Filters{})
		if err != nil || len(hits) != 0 {
			t.Fatalf("not configured must be empty, not an error: %v %v", hits, err)
		}
	}
	if be.calls != 1 {
		t.Fatalf("calls=%d: the leg stays off for a while after 'not configured'", be.calls)
	}
	now = now.Add(2 * notConfiguredBackoff)
	_, _ = g.Search(context.Background(), "x", 5, Filters{})
	if be.calls != 2 {
		t.Fatalf("calls=%d: the leg is retried after the backoff", be.calls)
	}
}

func TestGRPCSemantic_WithoutDBUsesIndexedText(t *testing.T) {
	be := &fakeSearcher{hits: []grpcpipeline.SemanticHit{
		{SegmentID: 7, VideoID: "v", StartSec: 1, EndSec: 2, Score: 0.5, Text: "نص"},
	}}
	hits, err := NewGRPCSemantic(be, nil).Search(context.Background(), "x", 5, Filters{})
	if err != nil || len(hits) != 1 || hits[0].Snippet != "نص" || hits[0].SegmentID != 7 {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
}

// The handler fuses FTS and semantic hits: with the gRPC client wired, a
// semantic-only match shows up in hybrid results with its snippet.
func TestGRPCSemantic_InHybridSearch(t *testing.T) {
	db := semanticDB(t)
	be := &fakeSearcher{hits: []grpcpipeline.SemanticHit{{SegmentID: 4, VideoID: "v2", Score: 0.8}}}
	h := &Handler{DB: db, Semantic: NewGRPCSemantic(be, db), SemanticBudget: time.Second}
	sem, degraded := h.runSemantic(context.Background(), "عبادة", 10, Filters{})
	if degraded || len(sem) != 1 || sem[0].Snippet != "صوم رمضان" {
		t.Fatalf("sem=%+v degraded=%v", sem, degraded)
	}
	fused := RRFuse(nil, sem, 60)
	if len(fused) != 1 || fused[0].SegmentID != 4 || fused[0].VideoID != "v2" {
		t.Fatalf("fused=%+v", fused)
	}
}
