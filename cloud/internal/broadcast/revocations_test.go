package broadcast

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// vectorEntries is one entry of each kind, as story 50.25 §1's example list.
func vectorEntries() []Entry {
	until := vectorAt.Add(9 * time.Hour)
	ch := ChannelID("srv-1", "7")
	return []Entry{
		{ID: "r-91", Kind: KindChannel, Ref: ch, Action: ActionOffAir, Reason: ReasonNotice, Since: vectorAt},
		{ID: "r-92", Kind: KindPublisher, Ref: "pub-42", Action: ActionTerminate, Reason: ReasonRepeatInfringer, Since: vectorAt},
		{ID: "r-93", Kind: KindProgramme, Ref: ch, Action: ActionSlate, Reason: ReasonNotice, Since: vectorAt, Until: &until},
		{ID: "r-94", Kind: KindHome, Ref: "srv-9", Action: ActionSuspend, Reason: ReasonAbuse, Since: vectorAt},
	}
}

// T01: a list signs and verifies; a bad key, a wrong kid, a tampered body and another control
// type are refused.
func TestRevocationListSignsAndVerifies(t *testing.T) {
	k := testKey(t, "cloud-1")
	text, l, err := SignRevocations(k, 1842, vectorEntries(), vectorAt)
	if err != nil {
		t.Fatal(err)
	}
	if l.Version != 1842 || len(l.Entries) != 4 || !l.ExpiresAt.Equal(vectorAt.Add(48*time.Hour)) {
		t.Fatalf("signed list = %+v", l)
	}
	got, err := VerifyRevocations(pubkeys(k), text)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1842 || len(got.Entries) != 4 || got.Entries[2].Until == nil || got.Entries[1].Action != ActionTerminate {
		t.Fatalf("verified list = %+v", got)
	}

	other := testKey(t, "cloud-2")
	if _, err := VerifyRevocations(pubkeys(other), text); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("a reader without the kid: %v", err)
	}
	// the right kid with another key's bytes: a bad signature
	forged := pubkeys(other)
	forged[k.Kid] = forged[other.Kid]
	if _, err := VerifyRevocations(forged, text); !errors.Is(err, ErrBadSignature) {
		t.Errorf("a wrong key under the kid: %v", err)
	}
	body, sig, _ := strings.Cut(strings.TrimPrefix(text, ControlPrefix), ".")
	tampered := ControlPrefix + body[:len(body)-2] + "AA." + sig
	if _, err := VerifyRevocations(pubkeys(k), tampered); err == nil {
		t.Error("a tampered body verified")
	}
	plan, _ := SignControl(k, "plan", map[string]any{"pub": "p", "mode": "full", "version": 1}, vectorAt, time.Hour)
	if _, err := VerifyRevocations(pubkeys(k), plan); !errors.Is(err, ErrNotRevocations) {
		t.Errorf("a plan post: %v", err)
	}
	// a home's copy is another typ: each verifier takes only its own
	home, _, err := SignHomeRevocations(k, 1842, vectorEntries(), vectorAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRevocations(pubkeys(k), home); !errors.Is(err, ErrNotRevocations) {
		t.Errorf("a home's copy as the full list: %v", err)
	}
	if _, err := VerifyHomeRevocations(pubkeys(k), text); !errors.Is(err, ErrNotRevocations) {
		t.Errorf("the full list as a home's copy: %v", err)
	}
	if l, err := VerifyHomeRevocations(pubkeys(k), home); err != nil || l.Version != 1842 {
		t.Errorf("a home's copy: %+v %v", l, err)
	}
	if _, err := VerifyRevocations(pubkeys(k), "nonsense"); !errors.Is(err, ErrNotRevocations) {
		t.Errorf("nonsense: %v", err)
	}
}

// An expired copy still verifies (a home keeps enforcing it), and says it expired.
func TestAnExpiredCopyVerifiesAndSaysSo(t *testing.T) {
	k := testKey(t, "cloud-1")
	text, _, err := SignRevocations(k, 1, nil, vectorAt)
	if err != nil {
		t.Fatal(err)
	}
	l, err := VerifyRevocations(pubkeys(k), text)
	if err != nil {
		t.Fatal(err)
	}
	if l.Expired(vectorAt.Add(47*time.Hour)) || !l.Expired(vectorAt.Add(48*time.Hour)) {
		t.Fatalf("expiry at %s", l.ExpiresAt)
	}
	if l.Entries == nil {
		t.Fatal("an empty list has entries [] (the edge reads an array)")
	}
}

// T07 (cloud side): a programme entry past its until is no longer signed into the list.
func TestAnEndedProgrammeLeavesTheList(t *testing.T) {
	k := testKey(t, "cloud-1")
	_, l, err := SignRevocations(k, 2, vectorEntries(), vectorAt.Add(10*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if e.Kind == KindProgramme {
			t.Fatalf("an ended programme entry is still signed: %+v", e)
		}
	}
	if len(l.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(l.Entries))
	}
}

func TestEntryCombinations(t *testing.T) {
	until := vectorAt.Add(time.Hour)
	cases := []struct {
		name string
		e    Entry
		ok   bool
	}{
		{"channel off air", Entry{Kind: KindChannel, Ref: "c", Action: ActionOffAir, Reason: ReasonReport}, true},
		{"global off air", Entry{Kind: KindGlobal, Action: ActionOffAir, Reason: ReasonIncident}, true},
		{"publisher suspend", Entry{Kind: KindPublisher, Ref: "p", Action: ActionSuspend, Reason: ReasonAbuse}, true},
		{"home terminate", Entry{Kind: KindHome, Ref: "h", Action: ActionTerminate, Reason: ReasonAbuse}, true},
		{"programme slate", Entry{Kind: KindProgramme, Ref: "c", Action: ActionSlate, Reason: ReasonNotice, Until: &until}, true},
		{"programme without until", Entry{Kind: KindProgramme, Ref: "c", Action: ActionSlate, Reason: ReasonNotice}, false},
		{"slate on a channel", Entry{Kind: KindChannel, Ref: "c", Action: ActionSlate, Reason: ReasonNotice, Until: &until}, false},
		{"channel terminate", Entry{Kind: KindChannel, Ref: "c", Action: ActionTerminate, Reason: ReasonNotice}, false},
		{"global with a ref", Entry{Kind: KindGlobal, Ref: "x", Action: ActionOffAir, Reason: ReasonIncident}, false},
		{"global suspend", Entry{Kind: KindGlobal, Action: ActionSuspend, Reason: ReasonIncident}, false},
		{"no ref", Entry{Kind: KindHome, Action: ActionSuspend, Reason: ReasonAbuse}, false},
		{"unknown reason", Entry{Kind: KindHome, Ref: "h", Action: ActionSuspend, Reason: "because"}, false},
		{"unknown kind", Entry{Kind: "planet", Ref: "h", Action: ActionSuspend, Reason: ReasonAbuse}, false},
	}
	for _, c := range cases {
		if err := c.e.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if _, _, err := SignRevocations(testKey(t, "cloud-1"), 1, []Entry{cases[5].e}, vectorAt); !errors.Is(err, ErrBadEntry) {
		t.Errorf("a bad entry signed: %v", err)
	}
}

func TestPublicKeysParsesCloudPubkeys(t *testing.T) {
	k := testKey(t, "cloud-1")
	m, err := PublicKeys(PublicKeysJSON(k))
	if err != nil || len(m[k.Kid]) != 32 {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := PublicKeys(`{"x":"short"}`); err == nil {
		t.Error("a short key parsed")
	}
}
