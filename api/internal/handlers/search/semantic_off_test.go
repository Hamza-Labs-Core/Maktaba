package search

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Story 51.09 T11: with search.semantic off, a search does not ask the semantic leg (a
// backend that hangs costs nothing): the answer comes at once, not degraded, with
// "semantic": "off".
func TestSemanticOffSkipsTheLeg(t *testing.T) {
	stub := &stubSemantic{delay: 5 * time.Second, hits: []Hit{{SegmentID: 9}}}
	h := &Handler{Semantic: stub, SemanticBudget: 2 * time.Second, SemanticOn: func() bool { return false }}
	for _, mode := range []string{"semantic", "hybrid"} {
		rec := httptest.NewRecorder()
		start := time.Now()
		h.Search(rec, adminReq(`{"q":"q","mode":"`+mode+`"}`))
		if took := time.Since(start); took > 100*time.Millisecond {
			t.Fatalf("%s: took %v", mode, took)
		}
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `"semantic":"off"`) || strings.Contains(body, `"degraded":true`) {
			t.Fatalf("%s: %d %s", mode, rec.Code, body)
		}
	}
	if n := atomic.LoadInt32(&stub.calls); n != 0 {
		t.Fatalf("the semantic leg was asked %d times", n)
	}
	// on again: asked, and no "semantic" field
	stub.delay = 0
	h.SemanticOn = func() bool { return true }
	rec := httptest.NewRecorder()
	h.Search(rec, adminReq(`{"q":"q","mode":"semantic"}`))
	if atomic.LoadInt32(&stub.calls) != 1 || strings.Contains(rec.Body.String(), `"semantic":"off"`) {
		t.Fatalf("on: calls %d body %s", stub.calls, rec.Body)
	}
}
