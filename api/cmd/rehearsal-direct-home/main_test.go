package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/directaccess"
)

// The stand-in web port the direct-remote rehearsal checks against: the media pattern its
// sha256 is compared with (byte i is 'a' + i % 26, across write chunks), and the requests
// counted by how they came, its own /rehearsal/vias left out.
func TestTheStandInWebPort(t *testing.T) {
	u := &upstream{vias: map[string]int{}}
	get := func(path, via string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if via != "" {
			r.Header.Set(directaccess.ViaHeader, via)
		}
		w := httptest.NewRecorder()
		u.ServeHTTP(w, r)
		return w
	}
	const n = 70_000 // past one 64 KiB chunk
	body := get("/media?bytes=70000", "direct").Body.Bytes()
	if len(body) != n {
		t.Fatalf("%d bytes, want %d", len(body), n)
	}
	for i, b := range body {
		if b != byte('a'+i%26) {
			t.Fatalf("byte %d is %q", i, b)
		}
	}
	if w := get("/media?bytes=-1", "direct"); w.Code != http.StatusBadRequest {
		t.Errorf("negative bytes: %d", w.Code)
	}
	if w := get("/api/health", "relay"); w.Body.String() != "ok" {
		t.Errorf("health %q", w.Body.String())
	}
	var vias map[string]int
	if err := json.Unmarshal(get("/rehearsal/vias", "").Body.Bytes(), &vias); err != nil {
		t.Fatal(err)
	}
	if vias["direct"] != 2 || vias["relay"] != 1 || len(vias) != 2 {
		t.Errorf("vias %v", vias)
	}
}
