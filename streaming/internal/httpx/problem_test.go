package httpx

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWrite_SetsHeadersAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, 404, "not-found", "Not Found", "the resource was not on disk")
	if got, want := rec.Code, 404; got != want {
		t.Fatalf("status=%d want %d", got, want)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json; charset=utf-8" {
		t.Fatalf("content-type=%q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff missing")
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("json: %v", err)
	}
	if p.Status != 404 || p.Title != "Not Found" || p.Detail == "" {
		t.Fatalf("envelope=%+v", p)
	}
	if !strings.HasSuffix(p.Type, "/not-found") {
		t.Fatalf("type=%q", p.Type)
	}
}

func TestWriteSignedURLError_AllSubTypesHaveDetail(t *testing.T) {
	// Story 23.2 AC-3: expired → 403; everything else stays 401.
	wantStatus := map[string]int{
		SignedURLMissing:      401,
		SignedURLExpired:      403,
		SignedURLWrongAud:     401,
		SignedURLWrongSub:     401,
		SignedURLWrongLib:     401,
		SignedURLBadSignature: 401,
	}
	for c, want := range wantStatus {
		t.Run(c, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteSignedURLError(rec, c)
			if rec.Code != want {
				t.Fatalf("sub-type %q: status=%d want %d", c, rec.Code, want)
			}
			var p Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatalf("json: %v", err)
			}
			if p.Status != want {
				t.Fatalf("envelope status=%d want %d", p.Status, want)
			}
			if !strings.Contains(p.Type, "signed-url-"+c) {
				t.Fatalf("type=%q does not include sub-type %q", p.Type, c)
			}
			if p.Detail == "" {
				t.Fatalf("missing detail for %s", c)
			}
		})
	}
}

// Story 23.2 AC-3 focused regression: an expired signed-URL/manifest
// token must produce a clear 403, never 401.
func TestWriteSignedURLError_ExpiredIs403(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteSignedURLError(rec, SignedURLExpired)
	if rec.Code != 403 {
		t.Fatalf("expired status=%d want 403 (AC-3: not 401)", rec.Code)
	}
}

func TestWriteSignedURLError_UnknownSubType(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteSignedURLError(rec, "made-up")
	var p Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Detail == "" {
		t.Fatalf("default detail missing")
	}
}
