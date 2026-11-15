package broadcast

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

// revSigner signs lists as the cloud does (cloud/internal/broadcast.SignRevocations): a control
// post of typ "revocations" under a TEST key from a public label.
type revSigner struct {
	kid  string
	priv ed25519.PrivateKey
}

func newRevSigner(label string) revSigner {
	seed := sha256.Sum256([]byte("maktaba-broadcast TEST KEY, never for production: " + label))
	priv := ed25519.NewKeyFromSeed(seed[:])
	sum := sha256.Sum256(priv.Public().(ed25519.PublicKey))
	return revSigner{kid: hex.EncodeToString(sum[:8]), priv: priv}
}

// readVectors is the cloud's signed samples (cloud/internal/broadcast/testdata/vectors.json).
func readVectors(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../../cloud/internal/broadcast/testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (s revSigner) keys() map[string]ed25519.PublicKey {
	return map[string]ed25519.PublicKey{s.kid: s.priv.Public().(ed25519.PublicKey)}
}

func (s revSigner) sign(version int64, issued time.Time, entries ...RevEntry) string {
	return s.signAs("revocations-home", version, issued, entries...)
}

func (s revSigner) signAs(typ string, version int64, issued time.Time, entries ...RevEntry) string {
	if entries == nil {
		entries = []RevEntry{}
	}
	payload := map[string]any{"typ": typ, "kid": s.kid, "iat": issued.Unix(), "exp": issued.Add(48 * time.Hour).Unix(),
		"schema": 1, "version": version, "issued_at": issued.UTC(), "expires_at": issued.Add(48 * time.Hour).UTC(), "entries": entries}
	j, _ := json.Marshal(payload)
	body := base64.RawURLEncoding.EncodeToString(j)
	sig := ed25519.Sign(s.priv, []byte(revControlDomain+"."+body))
	return revControlPrefix + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type fakeRevSource struct {
	mu   sync.Mutex
	text string
	err  error
	n    int
}

func (f *fakeRevSource) Revocations(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return f.text, f.err
}

func (f *fakeRevSource) set(text string, err error) {
	f.mu.Lock()
	f.text, f.err = text, err
	f.mu.Unlock()
}

// The cloud's vectors verify here too: the home reads the same format the edge does.
func TestTheHomeVerifiesTheCloudsVector(t *testing.T) {
	raw := readVectors(t)
	var v struct {
		CloudPubkeys string `json:"cloud_pubkeys"`
		Revocations  string `json:"revocations"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	keys, err := ParseRevocationKeys(v.CloudPubkeys)
	if err != nil {
		t.Fatal(err)
	}
	l, err := VerifyRevocations(keys, v.Revocations)
	if err != nil || l.Version != 1842 || len(l.Entries) != 4 {
		t.Fatalf("the vector: %+v %v", l, err)
	}
}

func TestVerifyAndStopFor(t *testing.T) {
	s := newRevSigner("cloud-1")
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	until := at.Add(9 * time.Hour)
	text := s.sign(7, at,
		RevEntry{ID: "r-1", Kind: "channel", Ref: "edge-a", Action: "off_air", Reason: "notice"},
		RevEntry{ID: "r-2", Kind: "programme", Ref: "edge-b", Action: "slate", Reason: "notice", Until: &until},
		RevEntry{ID: "r-3", Kind: "publisher", Ref: "pub-9", Action: "terminate", Reason: "repeat_infringer"},
		RevEntry{ID: "r-4", Kind: "home", Ref: "srv-1", Action: "suspend", Reason: "abuse"},
	)
	l, err := VerifyRevocations(s.keys(), text)
	if err != nil {
		t.Fatal(err)
	}
	if st := l.StopFor("edge-a", "pub-1", "srv-2"); st == nil || st.Action != "off_air" || st.Kind != "channel" {
		t.Errorf("channel: %+v", st)
	}
	if st := l.StopFor("edge-b", "pub-1", "srv-2"); st != nil {
		t.Errorf("a programme's slate stops nothing at the home: %+v", st)
	}
	if st := l.StopFor("edge-a", "pub-9", "srv-1"); st == nil || st.Action != "terminate" {
		t.Errorf("the strongest wins: %+v", st)
	}
	if st := l.StopFor("", "", "srv-1"); st == nil || st.Action != "suspend" || st.Reason != "abuse" {
		t.Errorf("home: %+v", st)
	}
	if !l.Expired(at.Add(48*time.Hour)) || l.Expired(at.Add(47*time.Hour)) {
		t.Error("expiry")
	}
	if _, err := VerifyRevocations(newRevSigner("cloud-2").keys(), text); !errors.Is(err, ErrRevocationSignature) {
		t.Errorf("an unknown key: %v", err)
	}
	if _, err := VerifyRevocations(s.keys(), text[:len(text)-3]+"AAA"); !errors.Is(err, ErrRevocationSignature) {
		t.Errorf("a bad signature: %v", err)
	}
	// the full list (the edge's typ) reads too; another control post does not
	if l, err := VerifyRevocations(s.keys(), s.signAs("revocations", 7, at)); err != nil || l.Version != 7 {
		t.Errorf("the full list: %+v %v", l, err)
	}
	if _, err := VerifyRevocations(s.keys(), s.signAs("plan", 7, at)); err == nil {
		t.Error("a plan read as a list")
	}
	if _, err := ParseRevocationKeys(`{"k":"short"}`); err == nil {
		t.Error("a short key parsed")
	}
}

// Anti-rollback without a database: a newer list replaces, an older is refused, an equal one
// refreshes the copy (its new expiry).
func TestAcceptRefusesAnOlderList(t *testing.T) {
	s := newRevSigner("cloud-1")
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	r := &Revocations{Keys: s.keys(), Source: &fakeRevSource{}, Now: func() time.Time { return now }}
	ctx := context.Background()
	if err := r.Accept(ctx, s.sign(1842, now)); err != nil {
		t.Fatal(err)
	}
	if err := r.Accept(ctx, s.sign(1841, now)); !errors.Is(err, ErrRevocationOlder) {
		t.Fatalf("1841 after 1842: %v", err)
	}
	if r.Current(ctx).Version != 1842 {
		t.Fatal("1842 did not stay")
	}
	later := now.Add(30 * time.Hour)
	if err := r.Accept(ctx, s.sign(1842, later)); err != nil {
		t.Fatal(err)
	}
	if !r.Current(ctx).ExpiresAt.Equal(later.Add(48 * time.Hour).UTC()) {
		t.Fatalf("an equal version's fresh expiry was not kept: %s", r.Current(ctx).ExpiresAt)
	}
	if err := r.Accept(ctx, newRevSigner("forger").sign(9999, now)); !errors.Is(err, ErrRevocationSignature) {
		t.Fatalf("a forged list: %v", err)
	}
	// no keys: the guard is off
	if (&Revocations{Source: &fakeRevSource{}}).Enabled() {
		t.Fatal("a guard with no keys is enabled")
	}
}

func TestTokenIdentity(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"ingest","pub":"pub-1","home":"srv-1"}`))
	if pub, home := tokenIdentity("mkbi1." + body + ".sig"); pub != "pub-1" || home != "srv-1" {
		t.Fatalf("%q %q", pub, home)
	}
	if pub, home := tokenIdentity("nonsense"); pub != "" || home != "" {
		t.Fatal("nonsense has an identity")
	}
}
