package viewers

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type vectorFile struct {
	CloudPubkeys string `json:"cloud_pubkeys"`
	Ingest       string `json:"ingest"`
	IngestClaims struct {
		Pub   string   `json:"pub"`
		Ch    string   `json:"ch"`
		Rungs []string `json:"rungs"`
		Home  string   `json:"home"`
		Kbps  int      `json:"kbps"`
		Iat   int64    `json:"iat"`
		Exp   int64    `json:"exp"`
	} `json:"ingest_claims"`
}

func vectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile("../../cloud/internal/broadcast/testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// The rehearsal signs ingest tokens byte for byte as the cloud does: the shared vector's token
// is reproduced from its claims with the TEST key "cloud-1", whose public half the edge trusts.
func TestIngestTokenMatchesTheCloudsVector(t *testing.T) {
	v := vectors(t)
	key := CloudTestKey("cloud-1")
	var pubs map[string]string
	if err := json.Unmarshal([]byte(v.CloudPubkeys), &pubs); err != nil {
		t.Fatal(err)
	}
	if _, ok := pubs[Kid(key.Public().(ed25519.PublicKey))]; !ok {
		t.Fatalf("cloud-1's kid %s is not in the vectors' keys", Kid(key.Public().(ed25519.PublicKey)))
	}
	c := v.IngestClaims
	got, err := IngestToken(key, IngestClaims{Pub: c.Pub, Channel: c.Ch, Home: c.Home, Rungs: c.Rungs, Kbps: c.Kbps,
		IssuedAt: time.Unix(c.Iat, 0), TTL: time.Duration(c.Exp-c.Iat) * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got != v.Ingest {
		t.Fatalf("ingest token differs from the cloud's vector\n got %s\nwant %s", got, v.Ingest)
	}
}

func TestRevocationListAndViewerToken(t *testing.T) {
	key := CloudTestKey("cloud-1")
	list, err := RevocationList(key, 7, []RevocationEntry{{ID: "r-1", Kind: "channel", Ref: "abc", Action: "off_air", Reason: "notice"}}, time.Unix(1794528000, 0))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(list, ControlPrefix), ".")
	if !strings.HasPrefix(list, ControlPrefix) || len(parts) != 2 {
		t.Fatalf("list: %s", list)
	}
	sig, _ := b64.DecodeString(parts[1])
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), []byte(ControlDomain+"."+parts[0]), sig) {
		t.Fatal("the list's signature does not verify")
	}
	raw, _ := b64.DecodeString(parts[0])
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	if p["typ"] != "revocations" || p["version"].(float64) != 7 || p["exp"].(float64)-p["iat"].(float64) != 48*3600 {
		t.Fatalf("payload: %v", p)
	}
	tok, err := ViewerToken(RehearsalViewerKey, ViewerClaims{Pub: "pub-1", Channel: "abc", Session: "s1", Mode: "full", Exp: 1})
	if err != nil || !strings.HasPrefix(tok, ViewerPrefix) || strings.Count(tok, ".") != 2 {
		t.Fatalf("viewer token: %s %v", tok, err)
	}
	if _, err := ViewerToken("!!", ViewerClaims{}); err == nil {
		t.Fatal("a bad key was taken")
	}
}
