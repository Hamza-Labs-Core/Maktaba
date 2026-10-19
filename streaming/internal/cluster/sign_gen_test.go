package cluster

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The placement generation is part of the front-door signature: a request whose
// generation header (or fg parameter) was changed after signing is refused.
func TestSignatureCoversPlacementGeneration(t *testing.T) {
	k := NewKeyring([]byte("cluster-key"))
	path := "/stream/channel/x/live.ts"
	req := httptest.NewRequest("GET", path, nil)
	k.SignRequest(req, 7)
	if err := k.Verify(req, false); err != nil || PlacementGen(req) != 7 {
		t.Fatalf("signed request: %v gen %d", err, PlacementGen(req))
	}
	req.Header.Set(HeaderPlacement, "8")
	if err := k.Verify(req, false); err == nil {
		t.Fatal("a changed generation header verified")
	}
	req.Header.Del(HeaderPlacement)
	if err := k.Verify(req, false); err == nil {
		t.Fatal("a dropped generation header verified")
	}
	// no generation: signed as 0
	req = httptest.NewRequest("GET", path, nil)
	k.SignRequest(req, 0)
	if err := k.Verify(req, false); err != nil || PlacementGen(req) != 0 {
		t.Fatalf("gen 0: %v", err)
	}
	req.Header.Set(HeaderPlacement, "3")
	if err := k.Verify(req, false); err == nil {
		t.Fatal("an added generation header verified")
	}
	// the query form (redirected segments)
	seg := "/stream/channel/x/seg-1.ts"
	q := k.SignedQuery(seg, 5)
	ok := httptest.NewRequest("GET", seg+"?"+q, nil)
	if err := k.Verify(ok, true); err != nil || PlacementGen(ok) != 5 {
		t.Fatalf("query form: %v gen %d", err, PlacementGen(ok))
	}
	bad := httptest.NewRequest("GET", seg+"?"+strings.Replace(q, QueryPlacement+"=5", QueryPlacement+"=6", 1), nil)
	if err := k.Verify(bad, true); err == nil {
		t.Fatal("a changed fg parameter verified")
	}
	// a header signature does not take its generation from the query
	mixed := httptest.NewRequest("GET", path+"?"+QueryPlacement+"=9", nil)
	k.SignRequest(mixed, 7)
	if PlacementGen(mixed) != 7 {
		t.Fatalf("mixed: gen %d", PlacementGen(mixed))
	}
	if err := k.Verify(httptest.NewRequest("GET", path, nil), false); err == nil {
		t.Fatal("unsigned verified")
	}
	bogus := httptest.NewRequest("GET", path, nil)
	k.SignRequest(bogus, 1)
	bogus.Header.Set(HeaderPlacement, "x")
	if err := k.Verify(bogus, false); err == nil {
		t.Fatal("a non-numeric generation verified")
	}
}

// The API's copy of the signature (api/internal/handlers/tvcluster.Sign) is
// HMAC over "METHOD\npath\nexp\n0": pin the bytes so the two stay in step.
func TestSignatureBytesForTheAPI(t *testing.T) {
	k := NewKeyring([]byte("k"))
	sig := k.Sign("POST", "/internal/tv/place/x", SignTTL)
	exp, sum, _ := strings.Cut(sig, ".")
	var e int64
	for _, c := range exp {
		e = e*10 + int64(c-'0')
	}
	if sum != mac([]byte("k"), "POST", "/internal/tv/place/x", e, 0) {
		t.Fatal("signature bytes changed")
	}
}
