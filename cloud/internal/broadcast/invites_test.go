package broadcast

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func goodInvite() InviteClaims {
	return InviteClaims{Pub: "srv-1", Chs: []string{ChannelID("srv-1", "news")}, Inv: "inv-1", Max: 2, Exp: vectorAt.Add(DefaultInviteTTL).Unix()}
}

func TestInviteIssueAndVerify(t *testing.T) {
	k := testKey(t, "cloud-1")
	tok, err := IssueInvite(k, goodInvite(), vectorAt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, InvitePrefix) {
		t.Fatalf("prefix: %s", tok)
	}
	c, err := VerifyInvite(pubkeys(k), tok, vectorAt)
	if err != nil {
		t.Fatal(err)
	}
	if c.Typ != "invite" || c.Pub != "srv-1" || c.Max != 2 || c.Kid != k.Kid || c.Iat != vectorAt.Unix() {
		t.Fatalf("claims %+v", c)
	}
	// expired, another key, tampered, and an ingest token passing as an invite all fail
	if _, err := VerifyInvite(pubkeys(k), tok, vectorAt.Add(31*24*time.Hour)); err == nil {
		t.Fatal("an expired invite verified")
	}
	if _, err := VerifyInvite(pubkeys(testKey(t, "cloud-2")), tok, vectorAt); err == nil {
		t.Fatal("another key verified")
	}
	if _, err := VerifyInvite(pubkeys(k), tok[:len(tok)-2]+"AA", vectorAt); err == nil {
		t.Fatal("a tampered invite verified")
	}
	ing, _ := IssueIngest(k, goodClaims(), vectorAt)
	if _, err := VerifyInvite(pubkeys(k), InvitePrefix+strings.TrimPrefix(ing, IngestPrefix), vectorAt); err == nil {
		t.Fatal("an ingest signature passed as an invite")
	}
	// all channels: an empty list, never null
	all := goodInvite()
	all.Chs = nil
	tok, _ = IssueInvite(k, all, vectorAt)
	body, _, _ := strings.Cut(strings.TrimPrefix(tok, InvitePrefix), ".")
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	if !bytes.Contains(raw, []byte(`"chs":[]`)) {
		t.Fatalf("all channels: %s", raw)
	}
}

func TestInviteBounds(t *testing.T) {
	k := testKey(t, "cloud-1")
	for name, mod := range map[string]func(*InviteClaims){
		"no publisher": func(c *InviteClaims) { c.Pub = "" },
		"no id":        func(c *InviteClaims) { c.Inv = "" },
		"no uses":      func(c *InviteClaims) { c.Max = 0 },
		"501 uses":     func(c *InviteClaims) { c.Max = 501 },
		"past":         func(c *InviteClaims) { c.Exp = vectorAt.Unix() },
		"over a year":  func(c *InviteClaims) { c.Exp = vectorAt.Add(MaxInviteTTL + time.Hour).Unix() },
		"33 channels":  func(c *InviteClaims) { c.Chs = make([]string, 33) },
	} {
		c := goodInvite()
		mod(&c)
		if _, err := IssueInvite(k, c, vectorAt); !errors.Is(err, ErrBadInvite) {
			t.Errorf("%s: %v", name, err)
		}
	}
	c := goodInvite()
	c.Max, c.Exp = MaxInviteUses, vectorAt.Add(MaxInviteTTL).Unix()
	if _, err := IssueInvite(k, c, vectorAt); err != nil {
		t.Fatalf("the most an invite may be: %v", err)
	}
}

func TestShortCodes(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := NewShortCode()
		if err != nil || !ValidShortCode(c) {
			t.Fatalf("%q %v", c, err)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("codes repeat too often: %d of 200", len(seen))
	}
	for _, bad := range []string{"MK-abcd", "MK-ABC", "XX-ABCD", "MK-ABCDE", "MK-AB D"} {
		if ValidShortCode(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSignAccess(t *testing.T) {
	k := testKey(t, "cloud-1")
	tok, err := SignAccess(k, accessFixture(), vectorAt)
	if err != nil {
		t.Fatal(err)
	}
	body, _, _ := strings.Cut(strings.TrimPrefix(tok, ControlPrefix), ".")
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["typ"] != "access" || m["pub"] != "srv-1" || m["version"].(float64) != 4 || m["exp"].(float64) != float64(vectorAt.Add(AccessTTL).Unix()) {
		t.Fatalf("payload %v", m)
	}
}

func accessFixture() AccessState {
	return AccessState{Pub: "srv-1", Version: 4, OpenAllowed: true, MaxSessionsPerIP: 3, DailyMinutes: 0,
		Chans:          map[string]AccessChannel{ChannelID("srv-1", "news"): {Access: "invite"}, ChannelID("srv-1", "sport"): {Access: "open", MaxViewers: 50}},
		RevokedInvites: []string{"inv-revoked"}, RevokedPasses: []string{}, Codes: map[string]string{}}
}

// inviteVectors are the edge's (web/packages/broadcast-edge/test/access.test.ts).
type inviteVectors struct {
	Note   string `json:"note"`
	AtMs   int64  `json:"at_ms"`
	Invite string `json:"invite"`
	Access string `json:"access"`
}

func makeInviteVectors(t *testing.T) []byte {
	k := testKey(t, "cloud-1")
	v := inviteVectors{Note: "TEST keys from public labels (tokens_test.go), never for production. go test ./internal/broadcast -run TestInviteVectors -update rewrites this file.", AtMs: vectorAt.UnixMilli()}
	var err error
	if v.Invite, err = IssueInvite(k, goodInvite(), vectorAt); err != nil {
		t.Fatal(err)
	}
	a := accessFixture()
	a.Codes = map[string]string{"MK-AB12": v.Invite}
	if v.Access, err = SignAccess(k, a, vectorAt); err != nil {
		t.Fatal(err)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func TestInviteVectors(t *testing.T) {
	want := makeInviteVectors(t)
	if *update {
		if err := os.WriteFile("testdata/invite_vectors.json", want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile("testdata/invite_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("testdata/invite_vectors.json is stale: go test ./internal/broadcast -run TestInviteVectors -update")
	}
}
