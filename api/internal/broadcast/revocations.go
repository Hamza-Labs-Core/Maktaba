package broadcast

// The home's copy of Maktaba's broadcast revocation list (Epic 50 story 50.25 §1, §2). The cloud
// signs it with the broadcast signing key (cloud/internal/broadcast/revocations.go, the same
// format); the home checks it with the public keys it holds, keeps the newest version it has seen
// (an older one is refused) and honours it:
//
//   - a channel it names (off_air), or the home, its publisher or everything (suspend,
//     terminate, a global off_air): the channel stops uploading and the Broadcast page says why;
//   - a programme's slate: nothing here (the edge shows the slate; the home keeps uploading);
//   - a copy past its expiry (48 h): still enforced, a warning shows, and a new publish waits
//     until the list is refreshed; published channels keep running (a cloud outage must not
//     stop every broadcast).
//
// The list arrives through the relay agent (a poll every 10 minutes); the edge's revoked event
// on each channel's control socket (streaming) reaches the home even when the cloud link is down.
// The home's honouring is for the publisher's information: the edge refuses a revoked ingest
// whatever the home does.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	revControlPrefix = "mkbc1."
	revControlDomain = "maktaba-broadcast-control-v1"
	// RevocationsPoll is how often the home asks for the list (§2).
	RevocationsPoll = 10 * time.Minute
	// revocationsSetting is the app_settings row holding the newest signed copy.
	revocationsSetting = "broadcast.revocations"
	// listDetail marks a channel the home's list took off air (an edge revoked event has none),
	// so a later list lifts only what a list put on.
	listDetail = "list:"
)

// BuiltinRevocationKeys are the broadcast signing keys this release trusts (kid → base64url raw
// Ed25519 public key), the same set as the edge's CLOUD_PUBKEYS. A new kid is added a release
// before the cloud signs with it, the old one removed a release later (§ key rotation).
// MAKTABA_BROADCAST_PUBKEYS adds keys (staging, tests). With no key at all the home does not
// honour a list: nothing could tell it from a forgery.
var BuiltinRevocationKeys = map[string]string{}

// ParseRevocationKeys reads {"<kid>": "<base64url raw key>"}.
func ParseRevocationKeys(text string) (map[string]ed25519.PublicKey, error) {
	out := map[string]ed25519.PublicKey{}
	if strings.TrimSpace(text) == "" {
		return out, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		return nil, fmt.Errorf("broadcast: revocation keys: %w", err)
	}
	for kid, v := range m {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(v, "="))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("broadcast: revocation key %s is not a raw Ed25519 key", kid)
		}
		out[kid] = raw
	}
	return out, nil
}

// RevocationKeys is the built-in set plus extra (MAKTABA_BROADCAST_PUBKEYS).
func RevocationKeys(extra string) (map[string]ed25519.PublicKey, error) {
	b, _ := json.Marshal(BuiltinRevocationKeys)
	keys, err := ParseRevocationKeys(string(b))
	if err != nil {
		return nil, err
	}
	more, err := ParseRevocationKeys(extra)
	if err != nil {
		return nil, err
	}
	for k, v := range more {
		keys[k] = v
	}
	return keys, nil
}

// RevEntry is one entry of the list.
type RevEntry struct {
	ID     string     `json:"id"`
	Kind   string     `json:"kind"`
	Ref    string     `json:"ref,omitempty"`
	Action string     `json:"action"`
	Reason string     `json:"reason"`
	Until  *time.Time `json:"until,omitempty"`
}

// RevList is the signed list's payload.
type RevList struct {
	Schema    int        `json:"schema"`
	Version   int64      `json:"version"`
	IssuedAt  time.Time  `json:"issued_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	Entries   []RevEntry `json:"entries"`
}

var (
	// ErrRevocationSignature is a list that does not verify (an unknown kid or a bad signature).
	ErrRevocationSignature = errors.New("broadcast: revocation list signature")
	// ErrRevocationOlder is a list older than the newest the home has seen (anti-rollback).
	ErrRevocationOlder = errors.New("broadcast: revocation list older than the one held")
	errNotRevocations  = errors.New("broadcast: not a revocation list")
)

// VerifyRevocations checks a signed list (an expired copy verifies; Expired says so).
func VerifyRevocations(keys map[string]ed25519.PublicKey, text string) (RevList, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), revControlPrefix)
	if !ok {
		return RevList{}, errNotRevocations
	}
	body, sigText, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(sigText, ".") {
		return RevList{}, errNotRevocations
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return RevList{}, errNotRevocations
	}
	var head struct {
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return RevList{}, errNotRevocations
	}
	pub, ok := keys[head.Kid]
	sig, err := base64.RawURLEncoding.DecodeString(sigText)
	if !ok || err != nil || !ed25519.Verify(pub, []byte(revControlDomain+"."+body), sig) {
		return RevList{}, ErrRevocationSignature
	}
	var l RevList
	// the cloud sends a home its filtered copy under "revocations-home" (the edge takes only the
	// full "revocations"); a home reads either
	if (head.Typ != "revocations" && head.Typ != "revocations-home") || json.Unmarshal(raw, &l) != nil || l.Schema != 1 {
		return RevList{}, errNotRevocations
	}
	return l, nil
}

// Expired reports whether the copy is past its expiry at t.
func (l RevList) Expired(t time.Time) bool { return !t.Before(l.ExpiresAt) }

// Stop is what the list says stops a channel: its entry's action and reason.
type Stop struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
	// Kind is what the entry named: channel, home, publisher or global.
	Kind string `json:"kind"`
}

var actionRank = map[string]int{"off_air": 1, "suspend": 2, "terminate": 3}

// StopFor is the strongest entry that stops a channel (its edge id, the publisher and the home
// as the ingest token names them; any may be ""). A programme's slate stops nothing here.
func (l RevList) StopFor(edgeChannel, pub, home string) *Stop {
	var best *Stop
	for _, e := range l.Entries {
		if actionRank[e.Action] == 0 {
			continue
		}
		hit := false
		switch e.Kind {
		case "global":
			hit = true
		case "channel":
			hit = edgeChannel != "" && e.Ref == edgeChannel
		case "publisher":
			hit = pub != "" && e.Ref == pub
		case "home":
			hit = home != "" && e.Ref == home
		}
		if hit && (best == nil || actionRank[e.Action] > actionRank[best.Action]) {
			best = &Stop{Action: e.Action, Reason: e.Reason, Kind: e.Kind}
		}
	}
	return best
}

// RevocationSource fetches the signed list (the relay agent; a fake in tests).
type RevocationSource interface {
	Revocations(ctx context.Context) (string, error)
}

// Revocations is the home's guard: the newest copy, its refresh and what it stops.
type Revocations struct {
	Store  *Store
	Source RevocationSource
	Keys   map[string]ed25519.PublicKey
	Now    func() time.Time
	Logger *slog.Logger

	mu       sync.Mutex
	loaded   bool
	list     *RevList
	text     string
	fetched  time.Time
	lastPoll time.Time
}

// Enabled is a guard with keys and a source: without keys no list can be trusted.
func (r *Revocations) Enabled() bool { return r != nil && len(r.Keys) > 0 && r.Source != nil }

func (r *Revocations) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Revocations) log() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

type savedList struct {
	Text      string    `json:"text"`
	FetchedAt time.Time `json:"fetched_at"`
}

// load reads the saved copy once (a restart keeps enforcing it and keeps its anti-rollback).
func (r *Revocations) load(ctx context.Context) {
	if r.loaded || r.Store == nil || r.Store.DB == nil {
		r.loaded = true
		return
	}
	r.loaded = true
	var raw string
	if err := r.Store.DB.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = $1`, revocationsSetting).Scan(&raw); err != nil {
		return
	}
	var s savedList
	if json.Unmarshal([]byte(raw), &s) != nil {
		return
	}
	if l, err := VerifyRevocations(r.Keys, s.Text); err == nil {
		r.list, r.text, r.fetched = &l, s.Text, s.FetchedAt
	}
}

// Accept takes a signed list: verified, not older than the one held, then kept and saved.
func (r *Revocations) Accept(ctx context.Context, text string) error {
	l, err := VerifyRevocations(r.Keys, text)
	if err != nil {
		if errors.Is(err, ErrRevocationSignature) {
			r.log().Error("broadcast: revocation list refused: bad signature", "event", "broadcast_revocations_bad_signature", "alert", "SEV2")
		}
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load(ctx)
	if r.list != nil && l.Version < r.list.Version {
		r.log().Warn("broadcast: an older revocation list was refused", "version", l.Version, "held", r.list.Version,
			"event", "broadcast_revocations_rollback")
		return ErrRevocationOlder
	}
	// an equal version is a fresh copy of the same list: its new expiry is kept
	r.list, r.text, r.fetched = &l, text, r.now()
	if r.Store != nil && r.Store.DB != nil {
		b, _ := json.Marshal(savedList{Text: text, FetchedAt: r.fetched})
		if _, err := r.Store.DB.ExecContext(ctx, `
			INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2::jsonb, now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, revocationsSetting, string(b)); err != nil {
			r.log().Warn("broadcast: the revocation list was not saved", "err", err)
		}
	}
	return nil
}

// Refresh asks the source for the list and accepts it.
func (r *Revocations) Refresh(ctx context.Context) error {
	text, err := r.Source.Revocations(ctx)
	if err != nil {
		return err
	}
	return r.Accept(ctx, text)
}

// Poll refreshes when the last ask is RevocationsPoll old (or none was made).
func (r *Revocations) Poll(ctx context.Context) {
	r.mu.Lock()
	due := r.lastPoll.IsZero() || r.now().Sub(r.lastPoll) >= RevocationsPoll
	if due {
		r.lastPoll = r.now()
	}
	r.mu.Unlock()
	if !due {
		return
	}
	if err := r.Refresh(ctx); err != nil && !errors.Is(err, ErrRevocationSignature) {
		r.log().Warn("broadcast: the revocation list was not refreshed", "err", err, "event", "broadcast_revocations_refresh_failed")
	}
}

// Current is the copy held (nil: none yet).
func (r *Revocations) Current(ctx context.Context) *RevList {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load(ctx)
	return r.list
}

// RevocationStatus is what the Broadcast page shows of the list.
type RevocationStatus struct {
	Version   int64      `json:"version"`
	FetchedAt *time.Time `json:"fetched_at,omitempty"`
	// Stale is a copy past its expiry, or none: new publishes wait (broadcast.revocationsStale).
	Stale bool `json:"stale"`
	// Days is how many whole days the home has gone without a fresh copy.
	Days int `json:"days"`
	// Home is an entry stopping every channel here (the home, its publisher or everything).
	Home *Stop `json:"home,omitempty"`
}

// Status is the list's state for the page; pub and home are the ingest token's.
func (r *Revocations) Status(ctx context.Context, pub, home string) RevocationStatus {
	l := r.Current(ctx)
	t := r.now()
	if l == nil {
		return RevocationStatus{Stale: true}
	}
	st := RevocationStatus{Version: l.Version, Stale: l.Expired(t)}
	r.mu.Lock()
	f := r.fetched
	r.mu.Unlock()
	if !f.IsZero() {
		st.FetchedAt = &f
		if st.Stale {
			st.Days = int(t.Sub(f) / (24 * time.Hour))
		}
	}
	// a stop that names no single channel: the home's, the publisher's or a global one
	if s := l.StopFor("", pub, home); s != nil {
		st.Home = s
	}
	return st
}

// tokenIdentity reads the publisher and home an ingest token names (the home's own token:
// its payload, no signature check needed).
func tokenIdentity(token string) (pub, home string) {
	rest, ok := strings.CutPrefix(token, "mkbi1.")
	if !ok {
		return "", ""
	}
	body, _, _ := strings.Cut(rest, ".")
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", ""
	}
	var c struct {
		Pub  string `json:"pub"`
		Home string `json:"home"`
	}
	_ = json.Unmarshal(raw, &c)
	return c.Pub, c.Home
}

// identity is this home's publisher and server id, from any of its ingest tokens.
func (s *Service) identity(ctx context.Context) (pub, home string) {
	tok, _, err := s.Store.AnyToken(ctx)
	if err != nil || tok == "" {
		return "", ""
	}
	return tokenIdentity(tok)
}

// revocationRefusal is a new publish the list forbids: a copy too old to trust, a stop on the
// home or publisher (suspended, terminated), or one on this channel.
func (s *Service) revocationRefusal(ctx context.Context, ch Channel) *Problem {
	r := s.Revocations
	if !r.Enabled() {
		return nil
	}
	l := r.Current(ctx)
	if l == nil || l.Expired(s.now()) {
		if err := r.Refresh(ctx); err != nil {
			s.log().Warn("broadcast: the revocation list could not be refreshed before a publish", "err", err)
		}
		l = r.Current(ctx)
	}
	pub, home := s.identity(ctx)
	if l == nil || l.Expired(s.now()) {
		return problem(409, "", "revocationsStale", map[string]any{"days": r.Status(ctx, pub, home).Days})
	}
	return stopProblem(l.StopFor(ch.EdgeChannel, pub, home))
}

func stopProblem(st *Stop) *Problem {
	switch {
	case st == nil:
		return nil
	case st.Action == "terminate":
		return problem(409, "", "terminated", map[string]any{"reason": st.Reason})
	case st.Kind == "channel":
		return problem(409, "", "revoked.channel", map[string]any{"reason": st.Reason})
	}
	return problem(409, "", "revoked.suspended", map[string]any{"reason": st.Reason})
}

// applyRevocations stops the published channels the list names and lifts what an earlier list
// stopped and this one no longer names. A channel the edge revoked (its control socket) is left
// to the edge's restored event.
func (s *Service) applyRevocations(ctx context.Context) {
	r := s.Revocations
	if !r.Enabled() {
		return
	}
	r.Poll(ctx)
	l := r.Current(ctx)
	if l == nil {
		return
	}
	chs, err := s.Store.List(ctx)
	if err != nil {
		return
	}
	pub, home := s.identity(ctx)
	for _, ch := range chs {
		st := l.StopFor(ch.EdgeChannel, pub, home)
		switch {
		case st != nil && (ch.Published() || ch.State == StateOffAir):
			_ = s.Store.SetState(ctx, ch.ID, StateRevoked, listDetail+st.Reason)
			s.log().Warn("broadcast: Maktaba took the channel off air", "channel_id", ch.ID, "action", st.Action,
				"reason", st.Reason, "event", "broadcast_revoked")
			s.notice(ctx, ch.ID, "revoked")
		case st == nil && ch.State == StateRevoked && strings.HasPrefix(ch.Detail, listDetail):
			_ = s.Store.SetState(ctx, ch.ID, StateLive, "")
			s.log().Info("broadcast: the channel's revocation was lifted", "channel_id", ch.ID, "event", "broadcast_restored")
		}
	}
}
