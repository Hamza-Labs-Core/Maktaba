package search

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
