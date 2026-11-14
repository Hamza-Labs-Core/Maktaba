package broadcast

// Viewer invites (Epic 50 story 50.26 §1): a publisher's invite link is a cloud-signed token the
// edge checks with CLOUD_PUBKEYS, as it checks ingest tokens:
//
//	invite  mkbn1.<base64url(payload JSON)>.<base64url(Ed25519 signature)>
//
// The payload binds the publisher (the home), the channels it opens (none listed: all of the
// publisher's), the invite's id, its use limit and its expiry. Its signature covers
// "maktaba-broadcast-invite-v1." + <base64url(payload)>, so an ingest token or a control post
// never passes as an invite. The edge counts the uses and holds the revocations (the access
// control post, AccessControl); the cloud only signs.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	InvitePrefix = "mkbn1."
	InviteDomain = "maktaba-broadcast-invite-v1"

	// DefaultInviteTTL and MaxInviteTTL bound an invite's life (30 days, up to a year).
	DefaultInviteTTL = 30 * 24 * time.Hour
	MaxInviteTTL     = 365 * 24 * time.Hour
	// DefaultInviteUses and MaxInviteUses bound the devices one invite admits (10, up to 500).
	DefaultInviteUses = 10
	MaxInviteUses     = 500
	// MaxInviteChannels bounds the channels one invite names (a publisher has at most a few).
	MaxInviteChannels = 32
	// MaxLabelLen bounds an invite's label ("Family in Cairo").
	MaxLabelLen = 80
)

// InviteClaims is what an invite binds.
type InviteClaims struct {
	Typ string `json:"typ"`
	// Pub is the publisher: the home's server id (the id the edge's PublisherControl and the
	// usage windows are keyed by).
	Pub string `json:"pub"`
	// Chs are the edge channel ids the invite opens; empty is every channel of the publisher.
	Chs []string `json:"chs"`
	// Inv is the invite's id (the revocation list and the use count name it).
	Inv string `json:"inv"`
	// Max is how many devices may redeem it.
	Max int    `json:"max"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Kid string `json:"kid"`
}

// ErrBadInvite is an invite the cloud refuses to sign (the API answers 400 before this).
var ErrBadInvite = errors.New("broadcast: bad invite")

// IssueInvite signs an invite. exp must be after now and at most MaxInviteTTL ahead; Max is
// 1..MaxInviteUses.
func IssueInvite(k Key, c InviteClaims, now time.Time) (string, error) {
	if c.Pub == "" || c.Inv == "" || c.Max < 1 || c.Max > MaxInviteUses || len(c.Chs) > MaxInviteChannels {
		return "", ErrBadInvite
	}
	if c.Exp <= now.Unix() || c.Exp > now.Add(MaxInviteTTL).Unix()+60 {
		return "", fmt.Errorf("%w: expiry", ErrBadInvite)
	}
	if c.Chs == nil {
		c.Chs = []string{}
	}
	c.Typ = "invite"
	c.Kid = k.Kid
	c.Iat = now.Unix()
	return sign(k, InvitePrefix, InviteDomain, c)
}

// VerifyInvite checks an invite's signature and expiry as the edge does (tests and tools).
func VerifyInvite(pubkeys map[string]ed25519.PublicKey, token string, now time.Time) (InviteClaims, error) {
	rest, ok := strings.CutPrefix(token, InvitePrefix)
	if !ok {
		return InviteClaims{}, errors.New("broadcast: not an invite")
	}
	body, sigText, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(sigText, ".") {
		return InviteClaims{}, errors.New("broadcast: malformed invite")
	}
	raw, err := b64.DecodeString(body)
	if err != nil {
		return InviteClaims{}, err
	}
	var c InviteClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return InviteClaims{}, err
	}
	pub, ok := pubkeys[c.Kid]
	if !ok {
		return InviteClaims{}, errors.New("broadcast: unknown key")
	}
	sig, err := b64.DecodeString(sigText)
	if err != nil || !ed25519.Verify(pub, []byte(InviteDomain+"."+body), sig) {
		return InviteClaims{}, errors.New("broadcast: bad signature")
	}
	if c.Typ != "invite" || now.Unix() >= c.Exp {
		return InviteClaims{}, errors.New("broadcast: expired or not an invite")
	}
	return c, nil
}

// codeAlphabet is the short codes' 36 characters: MK- plus 4 of them is about 1.68 M codes per
// publisher, with brute force bounded by the edge's 10 tries a minute per IP and its cap on a
// publisher's failed tries from all addresses together; a code also inherits its invite's use
// limit, expiry and revocation.
const codeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// NewShortCode makes a TV short code, "MK-" plus 4 characters.
func NewShortCode() (string, error) {
	var b strings.Builder
	b.WriteString("MK-")
	n := big.NewInt(int64(len(codeAlphabet)))
	for i := 0; i < 4; i++ {
		x, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b.WriteByte(codeAlphabet[x.Int64()])
	}
	return b.String(), nil
}

// ValidShortCode says whether s is a short code's shape (upper case, as NewShortCode makes).
func ValidShortCode(s string) bool {
	if len(s) != 7 || !strings.HasPrefix(s, "MK-") {
		return false
	}
	for _, r := range s[3:] {
		if !strings.ContainsRune(codeAlphabet, r) {
			return false
		}
	}
	return true
}

// AccessChannel is one channel's access as the edge enforces it.
type AccessChannel struct {
	// Access is "invite" or "open" (open only when the publisher may: AccessState.OpenAllowed).
	Access string `json:"access"`
	// MaxViewers is the publisher's per-channel cap (0: the publisher's limit only).
	MaxViewers int `json:"max_viewers"`
	// Indexable lets search engines index the channel's page (default false: noindex).
	Indexable bool `json:"indexable"`
}

// AccessState is a publisher's viewer access as the cloud pushes it to the edge (a control post
// of type "access", PUT /v1/control/access): the channels and their access, the per-IP and
// daily limits, the revoked invites and guest passes, and the short codes. Version only grows;
// the edge never replaces a newer state with an older one.
type AccessState struct {
	Pub     string                   `json:"pub"`
	Version int64                    `json:"version"`
	Chans   map[string]AccessChannel `json:"channels"`
	// OpenAllowed: the publisher is verified, its plan allows open, and
	// broadcast.open_access.enabled is on. Off, the edge treats every open channel as invite.
	OpenAllowed bool `json:"open_allowed"`
	// MaxSessionsPerIP is the open sessions one network may hold (default 3, up to 50); an
	// invite's guest pass is never counted.
	MaxSessionsPerIP int `json:"max_sessions_per_ip"`
	// DailyMinutes caps a guest pass's or an open session's viewing a day (0: none).
	DailyMinutes int `json:"daily_minutes"`
	// RevokedInvites and RevokedPasses stop new tokens at the next heartbeat.
	RevokedInvites []string `json:"revoked_invites"`
	RevokedPasses  []string `json:"revoked_passes"`
	// Codes maps each live short code to its invite token.
	Codes map[string]string `json:"codes"`
}

// AccessTTL is how long the edge accepts one access post; the cloud pushes again on every
// change and re-pushes every active publisher hourly (RefreshAll).
const AccessTTL = 24 * time.Hour

// SignAccess signs a publisher's access state as a control post.
func SignAccess(k Key, s AccessState, now time.Time) (string, error) {
	j, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		return "", err
	}
	return SignControl(k, "access", m, now, AccessTTL)
}
