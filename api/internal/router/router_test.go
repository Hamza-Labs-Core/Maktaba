package router

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
)

func TestNotFoundIsProblemJSON(t *testing.T) {
	r := New(Deps{IdempotencyStore: idempotency.NewMemoryStore()})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/no/such/route")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", got)
	}
	var body map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	if body["type"] != httperror.TypeNotFound {
		t.Fatalf("type = %v, want %s", body["type"], httperror.TypeNotFound)
	}
	// AC-2: every response carries X-Request-Id.
	if got := resp.Header.Get(reqid.Header); got == "" {
		t.Fatal("response missing X-Request-Id")
	}
}

func TestPanickingHandlerReturns500(t *testing.T) {
	r := New(Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	r.(*chi.Mux).Get("/boom", func(_ http.ResponseWriter, _ *http.Request) {
		panic("oh no")
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/boom")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "oh no") {
		t.Fatalf("panic value leaked into body: %s", b)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", got)
	}
}

func TestSystemVersionEndpoint(t *testing.T) {
	r := New(Deps{
		IdempotencyStore: idempotency.NewMemoryStore(),
		SchemaRev:        17,
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/system/version")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	if body["schema_revision"] != float64(17) {
		t.Fatalf("schema_revision = %v, want 17", body["schema_revision"])
	}
	if body["go_version"] == "" {
		t.Fatal("go_version not populated")
	}
}

func TestSystemHealthEndpointReturns200WithEmptyServices(t *testing.T) {
	r := New(Deps{IdempotencyStore: idempotency.NewMemoryStore()})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/system/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
