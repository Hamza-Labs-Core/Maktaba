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
