package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
)

// stubSemantic records call count and can be made slow or failing.
type stubSemantic struct {
	calls   int32
	delay   time.Duration
	err     error
	hits    []Hit
	gotCtx  context.Context
	blockCh chan struct{}
}

func (s *stubSemantic) Search(ctx context.Context, q string, k int, f Filters) ([]Hit, error) {
	atomic.AddInt32(&s.calls, 1)
	s.gotCtx = ctx
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.hits, nil
}

func adminReq(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/search", strings.NewReader(body))
	ctx := principal.WithPrincipal(req.Context(),
		&principal.Principal{UserID: "u1", IsAdmin: true, AccessAllLibraries: true})
	return req.WithContext(ctx)
}

// TestEmbedCacheHitAvoidsRecompute proves identical semantic queries are
// served from the in-process cache (HLB-333 AC2): the SemanticClient is
// called once, not twice.
func TestEmbedCacheHitAvoidsRecompute(t *testing.T) {
	stub := &stubSemantic{hits: []Hit{{SegmentID: 1, Snippet: "x"}}}
	h := &Handler{
		Semantic:   stub,
		EmbedCache: perf.NewCache[[]Hit]("search_embed", 100, time.Minute),
	}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.Search(rec, adminReq(`{"q":"hello world","mode":"semantic"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	if got := atomic.LoadInt32(&stub.calls); got != 1 {
		t.Fatalf("semantic called %d times, want 1 (cache miss then 2 hits)", got)
	}
	st := h.EmbedCache.Stats()
	if st.Hits != 2 || st.Misses != 1 {
		t.Fatalf("cache stats = %+v, want hits=2 misses=1", st)
	}
}

// TestSemanticDeadlineDegrades proves a slow semantic backend trips the
// per-request deadline, the response still returns (FTS-only), and the
// payload carries degraded:true (HLB-333 AC4).
func TestSemanticDeadlineDegrades(t *testing.T) {
	stub := &stubSemantic{delay: 500 * time.Millisecond, hits: []Hit{{SegmentID: 9}}}
	h := &Handler{
		Semantic:       stub,
		SemanticBudget: 20 * time.Millisecond,
	}
	rec := httptest.NewRecorder()
	start := time.Now()
	h.Search(rec, adminReq(`{"q":"slow query","mode":"semantic"}`))
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("request took %v; deadline not enforced", elapsed)
	}
	if !strings.Contains(rec.Body.String(), `"degraded":true`) {
		t.Fatalf("expected degraded:true, body=%s", rec.Body.String())
	}
}

// TestSemanticErrorDegrades proves a failing semantic backend degrades
// to FTS-only with degraded:true rather than silently swallowing.
func TestSemanticErrorDegrades(t *testing.T) {
	stub := &stubSemantic{err: errors.New("pipeline down")}
	h := &Handler{Semantic: stub}
	rec := httptest.NewRecorder()
	h.Search(rec, adminReq(`{"q":"q","mode":"semantic"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"degraded":true`) {
		t.Fatalf("expected degraded:true, body=%s", rec.Body.String())
	}
}

// freshHitsSemantic returns a brand-new []Hit (and fresh Hit values) on
// every call, so any snippet corruption observed by the tests below can
// only originate from the embed-cache boundary aliasing the live entry —
// never from the stub handing back a shared slice.
type freshHitsSemantic struct {
	calls int32
	hits  []Hit
}

func (s *freshHitsSemantic) Search(_ context.Context, _ string, _ int, _ Filters) ([]Hit, error) {
	atomic.AddInt32(&s.calls, 1)
	out := make([]Hit, len(s.hits))
	copy(out, s.hits)
	return out, nil
}

// TestEmbedCacheConcurrentSameKeyNoCorruption is the C1 regression guard.
//
// The cache-hit path used to return perf.Cache's live backing slice, and
// Search highlights Hit.Snippet IN PLACE. So:
//   - two concurrent identical mode=semantic requests raced on (and the
//     -race detector flags) the shared []Hit, and
//   - each subsequent hit re-highlighted already-<mark>'d text, producing
//     progressively nested <mark> (request #3 worse than #2).
//
// Both sub-tests below FAIL on the pre-fix code (revert the two
// append([]Hit(nil), …) boundary copies in search.go) and PASS after.
func TestEmbedCacheConcurrentSameKeyNoCorruption(t *testing.T) {
	// The stub Snippet CONTAINS the query term, so highlightSnippet
	// actually rewrites it (wraps "alpha" in <mark>…</mark>).
	const query = "alpha"
	const body = `{"q":"alpha","mode":"semantic"}`
	mkHandler := func() *Handler {
		return &Handler{
			Semantic: &freshHitsSemantic{
				hits: []Hit{{SegmentID: 1, Snippet: "the alpha segment text"}},
			},
			EmbedCache: perf.NewCache[[]Hit]("search_embed", 100, time.Minute),
		}
	}
	wantSnippet := highlightSnippet("the alpha segment text", query, 240)
	if !strings.Contains(wantSnippet, "<mark>alpha</mark>") {
		t.Fatalf("test setup: expected highlight to wrap term, got %q", wantSnippet)
	}
	if strings.Contains(wantSnippet, "<mark><mark>") || strings.Count(wantSnippet, "<mark>") != 1 {
		t.Fatalf("test setup: expected exactly one <mark>, got %q", wantSnippet)
	}

	// Decode the response and assert on the real Snippet string (the
	// raw body HTML-escapes "<" to "<", which obscures the
	// nesting). Exactly one <mark> wrapper, no nested/double <mark>.
	decodeSnippet := func(t *testing.T, label, body string) string {
		t.Helper()
		var resp Response
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("%s: unmarshal: %v body=%s", label, err, body)
		}
		if len(resp.Hits) != 1 {
			t.Fatalf("%s: want 1 hit, got %d body=%s", label, len(resp.Hits), body)
		}
		return resp.Hits[0].Snippet
	}
	assertWellFormed := func(t *testing.T, label, body string) {
		t.Helper()
		snip := decodeSnippet(t, label, body)
		if n := strings.Count(snip, "<mark>"); n != 1 {
			t.Fatalf("%s: expected exactly one <mark>, got %d in snippet=%q", label, n, snip)
		}
		if strings.Contains(snip, "<mark><mark>") || strings.Contains(snip, "</mark></mark>") {
			t.Fatalf("%s: nested/double <mark> (snippet corruption): snippet=%q", label, snip)
		}
		if !strings.Contains(snip, "<mark>alpha</mark>") {
			t.Fatalf("%s: expected <mark>alpha</mark>, snippet=%q", label, snip)
		}
	}

	// (a) TWO CONCURRENT identical requests: bodies must be
	// byte-identical and each well-formed. Under -race, aliasing the
	// shared cached []Hit is also a reported data race.
	t.Run("concurrent_same_key", func(t *testing.T) {
		h := mkHandler()
		var wg sync.WaitGroup
		bodies := make([]string, 2)
		codes := make([]int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rec := httptest.NewRecorder()
				h.Search(rec, adminReq(body))
				codes[i] = rec.Code
				bodies[i] = rec.Body.String()
			}(i)
		}
		wg.Wait()
		for i := 0; i < 2; i++ {
			if codes[i] != http.StatusOK {
				t.Fatalf("req %d: status=%d body=%s", i, codes[i], bodies[i])
			}
			assertWellFormed(t, "concurrent req "+string(rune('0'+i)), bodies[i])
		}
		if bodies[0] != bodies[1] {
			t.Fatalf("concurrent responses differ:\n#0=%s\n#1=%s", bodies[0], bodies[1])
		}
	})

	// (b) Sequential miss→hit→hit: the cached entry must stay
	// uncorrupted. Pre-fix, #3 carried doubly-<mark>'d snippets.
	t.Run("sequential_miss_then_hits", func(t *testing.T) {
		h := mkHandler()
		var b2, b3 string
		for i := 1; i <= 3; i++ {
			rec := httptest.NewRecorder()
			h.Search(rec, adminReq(body))
			if rec.Code != http.StatusOK {
				t.Fatalf("req #%d: status=%d body=%s", i, rec.Code, rec.Body.String())
			}
			switch i {
			case 2:
				b2 = rec.Body.String()
			case 3:
				b3 = rec.Body.String()
			}
		}
		assertWellFormed(t, "hit #2", b2)
		assertWellFormed(t, "hit #3", b3)
		if b2 != b3 {
			t.Fatalf("hit #2 and #3 differ (cached entry corrupted across hits):\n#2=%s\n#3=%s", b2, b3)
		}
	})
}

// TestHealthySemanticNotDegraded proves the happy path does NOT set the
// degraded flag.
func TestHealthySemanticNotDegraded(t *testing.T) {
	stub := &stubSemantic{hits: []Hit{{SegmentID: 1}}}
	h := &Handler{Semantic: stub}
	rec := httptest.NewRecorder()
	h.Search(rec, adminReq(`{"q":"q","mode":"semantic"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"degraded":true`) {
		t.Fatalf("did not expect degraded:true, body=%s", rec.Body.String())
	}
}
