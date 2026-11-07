package common

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadJSON_StrictUnknownFields(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"a","oops":"x"}`)
	r := httptest.NewRequest("POST", "/", body)
	var v struct {
		Name string `json:"name"`
	}
	if err := ReadJSON(r, &v, 0); err == nil {
		t.Fatal("expected unknown-field rejection")
	}
}

func TestReadJSON_TrailingDataRejected(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"a"} extra`)
	r := httptest.NewRequest("POST", "/", body)
	var v struct {
		Name string `json:"name"`
	}
	if err := ReadJSON(r, &v, 0); err == nil {
		t.Fatal("expected trailing-data rejection")
	}
}

func TestReadJSON_EmptyBodyIsBadRequest(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(""))
	var v struct{}
	err := ReadJSON(r, &v, 0)
	if err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("got %v", err)
	}
}

func TestQueryFloat_NaNRejected(t *testing.T) {
	r := httptest.NewRequest("GET", "/?from=NaN", nil)
	_, err := QueryFloat(r, "from", 0)
	if err == nil {
		t.Fatal("expected NaN rejection")
	}
}

func TestQueryCSV_DedupesAndTrims(t *testing.T) {
	r := httptest.NewRequest("GET", "/?tag=a,%20b,a,,c", nil)
	got := QueryCSV(r, "tag")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

// TestWritePassthroughNeverHTML: a captured body that echoes a script is relayed as
// text/plain with nosniff, and a JSON or problem+json body keeps its type (CodeQL
// go/reflected-xss, alerts 37 and 150).
func TestWritePassthroughNeverHTML(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "text/plain; charset=utf-8"},
		{"text/html; charset=utf-8", "text/plain; charset=utf-8"},
		{"application/json; charset=utf-8", "application/json"},
		{"application/problem+json", "application/problem+json"},
	} {
		h := http.Header{"X-Request-Id": {"r1"}}
		if tc.in != "" {
			h.Set("Content-Type", tc.in)
		}
		rec := httptest.NewRecorder()
		WritePassthrough(rec, 0, h, []byte("<script>alert(1)</script>"))
		if got := rec.Header().Get("Content-Type"); got != tc.want {
			t.Errorf("%q: Content-Type = %q, want %q", tc.in, got, tc.want)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Request-Id") != "r1" {
			t.Errorf("%q: headers = %v", tc.in, rec.Header())
		}
		if rec.Code != http.StatusOK || rec.Body.String() != "<script>alert(1)</script>" {
			t.Errorf("%q: %d %q", tc.in, rec.Code, rec.Body.String())
		}
	}
}
