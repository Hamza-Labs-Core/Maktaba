package broadcast

// The signed broadcast revocation list (Epic 50 story 50.25 §1): what the kill switch says to
// the edge and to homes. It is a control post (SignControl) of type "revocations", signed with
// the broadcast signing key (the key that signs ingest tokens, never the entitlement or plugin
// keys), whose payload is:
//
//	{"schema": 1, "version": 1842, "issued_at": "…", "expires_at": "…", "entries": [Entry, …]}
//
// The version only goes up (the edge and homes refuse an older list), entries are sticky (only
// a later version without the entry lifts it), and a copy lives 48 h: a home whose copy expired
// keeps enforcing it and refuses to start a new publish until it is refreshed.

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// RevocationsType is the control post's typ for the full list, the one the edge installs.
	RevocationsType = "revocations"
	// RevocationsHomeType is the typ of a home's filtered copy (ForHome). The edge installs only
	// RevocationsType, so a home cannot replay its own copy, which lacks the other homes'
	// entries, to the edge's control route as if it were the cloud's list.
	RevocationsHomeType = "revocations-home"
	// RevocationsSchema is the payload's schema number.
	RevocationsSchema = 1
	// RevocationsTTL is how long a signed copy is current (Epic 54 §6.3's 48 h).
	RevocationsTTL = 48 * time.Hour
)

// The entry kinds: what an entry names.
const (
	KindChannel   = "channel"   // ref: the edge's channel id (ChannelID)
	KindProgramme = "programme" // ref: a channel id; the slate airs until Until
	KindPublisher = "publisher" // ref: the publishing account
	KindHome      = "home"      // ref: the server id
	KindGlobal    = "global"    // no ref: every channel
)

// The actions, mildest first.
const (
	ActionSlate     = "slate"     // the channel's playlists show the slate (programme only)
	ActionOffAir    = "off_air"   // 503 off_air on play; the home stops that channel
	ActionSuspend   = "suspend"   // off_air for every channel of the home or publisher
	ActionTerminate = "terminate" // suspend, and the publisher can't publish again
)

// The reasons a publisher is shown (broadcast.reason.*) and the two only staff see.
const (
	ReasonNotice          = "notice"
	ReasonReport          = "report"
	ReasonIncident        = "incident"
	ReasonAbuse           = "abuse"
	ReasonRepeatInfringer = "repeat_infringer"
)

var (
	kinds   = []string{KindChannel, KindProgramme, KindPublisher, KindHome, KindGlobal}
	actions = []string{ActionSlate, ActionOffAir, ActionSuspend, ActionTerminate}
	reasons = []string{ReasonNotice, ReasonReport, ReasonIncident, ReasonAbuse, ReasonRepeatInfringer}

	// ErrBadEntry is an entry the list may not carry.
	ErrBadEntry = errors.New("broadcast: bad revocation entry")
	// ErrUnknownKey is a list signed with a kid the reader does not hold (or a retired one).
	ErrUnknownKey = errors.New("broadcast: unknown signing key")
	// ErrBadSignature is a list whose signature does not verify: refused and alerted.
	ErrBadSignature = errors.New("broadcast: bad signature")
	// ErrNotRevocations is a well-signed control post of another type, or a malformed one.
	ErrNotRevocations = errors.New("broadcast: not a revocation list")
)

// Entry is one revocation.
type Entry struct {
	ID     string     `json:"id"`
	Kind   string     `json:"kind"`
	Ref    string     `json:"ref,omitempty"`
	Action string     `json:"action"`
	Reason string     `json:"reason"`
	Since  time.Time  `json:"since"`
	Until  *time.Time `json:"until,omitempty"`
}

// Validate checks an entry's kind, action, reason and their combination: a programme entry is a
// slate with an end, a slate is only a programme's, a global entry is off_air with no ref, a
// channel is taken off air (suspend and terminate are a home's or a publisher's).
func (e Entry) Validate() error {
	bad := func(why string) error { return fmt.Errorf("%w: %s", ErrBadEntry, why) }
	switch {
	case !slices.Contains(kinds, e.Kind):
		return bad("kind")
	case !slices.Contains(actions, e.Action):
		return bad("action")
	case !slices.Contains(reasons, e.Reason):
		return bad("reason")
	case e.Kind == KindGlobal && (e.Ref != "" || e.Action != ActionOffAir):
		return bad("a global entry is off_air with no ref")
	case e.Kind != KindGlobal && (strings.TrimSpace(e.Ref) == "" || len(e.Ref) > 128):
		return bad("ref")
	case (e.Kind == KindProgramme) != (e.Action == ActionSlate):
		return bad("a slate is a programme's, and a programme is a slate")
	case e.Kind == KindProgramme && e.Until == nil:
		return bad("a programme entry needs until")
	case e.Kind == KindChannel && e.Action != ActionOffAir:
		return bad("a channel is taken off air")
	}
	return nil
}

// ActiveAt reports whether the entry holds at t: a programme's slate ends at Until.
func (e Entry) ActiveAt(t time.Time) bool { return e.Until == nil || t.Before(*e.Until) }

// RevocationList is the signed list's payload.
type RevocationList struct {
	Schema    int       `json:"schema"`
	Version   int64     `json:"version"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Entries   []Entry   `json:"entries"`
}

// Expired reports whether the copy is past its expiry at t (it is still enforced).
func (l RevocationList) Expired(t time.Time) bool { return !t.Before(l.ExpiresAt) }

// SignRevocations signs the list as of now: issued now, expiring RevocationsTTL later. Entries
// that ended (a programme past its until) are left out; the version is the caller's.
func SignRevocations(k Key, version int64, entries []Entry, now time.Time) (string, RevocationList, error) {
	return signRevocations(k, RevocationsType, version, entries, now)
}

// SignHomeRevocations signs a home's copy: as SignRevocations, under RevocationsHomeType.
func SignHomeRevocations(k Key, version int64, entries []Entry, now time.Time) (string, RevocationList, error) {
	return signRevocations(k, RevocationsHomeType, version, entries, now)
}

func signRevocations(k Key, typ string, version int64, entries []Entry, now time.Time) (string, RevocationList, error) {
	if version < 0 {
		return "", RevocationList{}, fmt.Errorf("broadcast: negative list version")
	}
	l := RevocationList{Schema: RevocationsSchema, Version: version, IssuedAt: now.UTC().Truncate(time.Second),
		ExpiresAt: now.Add(RevocationsTTL).UTC().Truncate(time.Second), Entries: []Entry{}}
	for _, e := range entries {
		if err := e.Validate(); err != nil {
			return "", RevocationList{}, fmt.Errorf("entry %s: %w", e.ID, err)
		}
		if e.ActiveAt(now) {
			l.Entries = append(l.Entries, e)
		}
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return "", RevocationList{}, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", RevocationList{}, err
	}
	text, err := SignControl(k, typ, payload, now, RevocationsTTL)
	return text, l, err
}

// VerifyRevocations checks a signed full list (RevocationsType) with the reader's keys (by kid)
// and returns it. An expired copy verifies: the reader decides what expiry means (a home keeps
// enforcing it). A home's copy (RevocationsHomeType) is ErrNotRevocations here.
func VerifyRevocations(pubkeys map[string]ed25519.PublicKey, text string) (RevocationList, error) {
	return verifyRevocations(pubkeys, text, RevocationsType)
}

// VerifyHomeRevocations checks a home's copy (RevocationsHomeType) as a home reads it.
func VerifyHomeRevocations(pubkeys map[string]ed25519.PublicKey, text string) (RevocationList, error) {
	return verifyRevocations(pubkeys, text, RevocationsHomeType)
}

func verifyRevocations(pubkeys map[string]ed25519.PublicKey, text, typ string) (RevocationList, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), ControlPrefix)
	if !ok {
		return RevocationList{}, ErrNotRevocations
	}
	body, sigText, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(sigText, ".") {
		return RevocationList{}, ErrNotRevocations
	}
	raw, err := b64.DecodeString(body)
	if err != nil {
		return RevocationList{}, ErrNotRevocations
	}
	var head struct {
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return RevocationList{}, ErrNotRevocations
	}
	pub, ok := pubkeys[head.Kid]
	if !ok {
		return RevocationList{}, ErrUnknownKey
	}
	sig, err := b64.DecodeString(sigText)
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, []byte(ControlDomain+"."+body), sig) {
		return RevocationList{}, ErrBadSignature
	}
	if head.Typ != typ {
		return RevocationList{}, ErrNotRevocations
	}
	var l RevocationList
	if err := json.Unmarshal(raw, &l); err != nil || l.Schema != RevocationsSchema {
		return RevocationList{}, ErrNotRevocations
	}
	for _, e := range l.Entries {
		if err := e.Validate(); err != nil {
			return RevocationList{}, fmt.Errorf("%w: %v", ErrNotRevocations, err)
		}
	}
	return l, nil
}

// PublicKeys parses a CLOUD_PUBKEYS value ({"<kid>": "<base64url raw key>"}).
func PublicKeys(text string) (map[string]ed25519.PublicKey, error) {
	var m map[string]string
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		return nil, fmt.Errorf("broadcast: public keys: %w", err)
	}
	out := make(map[string]ed25519.PublicKey, len(m))
	for kid, v := range m {
		raw, err := b64.DecodeString(strings.TrimRight(v, "="))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("broadcast: public key %s is not a raw Ed25519 key", kid)
		}
		out[kid] = raw
	}
	return out, nil
}
