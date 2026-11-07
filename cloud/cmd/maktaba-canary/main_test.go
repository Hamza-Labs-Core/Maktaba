package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The canary's API: health with a time, and the 10 MB file with every byte known.
func TestStub(t *testing.T) {
	srv := httptest.NewServer(Stub())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		OK bool  `json:"ok"`
		T  int64 `json:"t"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&h)
	_ = resp.Body.Close()
	if !h.OK || h.T < 1_700_000_000_000 {
		t.Errorf("health %+v", h)
	}
	resp, err = http.Get(srv.URL + "/canary/10mb.bin")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if len(b) != 10<<20 || b[27] != 'b' {
		t.Errorf("10mb.bin: %d bytes", len(b))
	}
}
