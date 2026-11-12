package signedurl

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
)

// AudChannel is the audience of a signed live channel URL (Epic 57, story 57.10):
//
//	/stream/channel/{channel_id}/…   aud=streaming-channel   sub=channel_id   usr=user_id
//	                                 fam=the sign-in's family   ac=its device's age limit
//
// A channel may have no library, so the token carries no lib claim: the streaming
// service checks that the subject is the channel in the path, and asks the API
// whether usr may watch what the channel plays.
const AudChannel = "streaming-channel"

// DefaultChannelTTL is how long a signed channel URL lasts: a player asks for a new
// one before it runs out.
const DefaultChannelTTL = 6 * time.Hour

// AdminTokenUsr is the usr of a channel URL signed for the admin token (an admin with
// no account). The streaming service takes it only with the signed is_admin claim.
const AdminTokenUsr = "admin"

// SignChannelURL signs a channel's stream URL for the account userID (a UUID: the
// streaming service asks the API about that account, and refuses a usr that names
// none). ttl is clamped to MaxTTL (0: DefaultChannelTTL). Returns the signed URL and
// its expiry.
func (m *Minter) SignChannelURL(rawURL, channelID, userID string, ttl time.Duration) (string, time.Time, error) {
	return m.SignChannelURLForSignIn(rawURL, channelID, userID, "", nil, ttl)
}

// SignChannelURLForSignIn is SignChannelURL for the sign-in the request came
// through: family is its refresh-token family (the access token's `fam`, "" when it
// has none) and ceiling its device's age limit (the `ac` claim, nil when none).
// The URL lives for hours and is fetched with no other credential, so it carries
// them: the streaming service hands them to the API with every question about the
// viewer, and the answer is held to the device's limit as well as the account's,
// as every other request of that sign-in is. Without them a child's device signed
// in with an unrestricted account would be judged by the account alone.
func (m *Minter) SignChannelURLForSignIn(rawURL, channelID, userID, family string, ceiling *int, ttl time.Duration) (string, time.Time, error) {
	if u, err := uuid.Parse(strings.TrimSpace(userID)); err != nil || u == uuid.Nil {
		return "", time.Time{}, errors.New("signedurl: a channel URL is signed for an account")
	}
	family = strings.ToLower(strings.TrimSpace(family))
	if family != "" {
		if _, err := uuid.Parse(family); err != nil {
			return "", time.Time{}, errors.New("signedurl: a sign-in's family is a UUID")
		}
	}
	return m.signChannel(rawURL, channelID, strings.ToLower(strings.TrimSpace(userID)), false, family, ceiling, ttl)
}

// SignChannelURLForAdminToken signs a channel's stream URL for the admin token: no
// account to ask about, so the token says so itself (usr admin, is_admin true). Only
// for a caller the API authenticated as the admin token.
func (m *Minter) SignChannelURLForAdminToken(rawURL, channelID string, ttl time.Duration) (string, time.Time, error) {
	return m.signChannel(rawURL, channelID, AdminTokenUsr, true, "", nil, ttl)
}

func (m *Minter) signChannel(rawURL, channelID, userID string, admin bool, family string, ceiling *int, ttl time.Duration) (string, time.Time, error) {
	if m == nil || m.set == nil || m.set.Active() == nil {
		return "", time.Time{}, ErrKeyUnavailable
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signedurl: %w", err)
	}
	channelID = strings.ToLower(strings.TrimSpace(channelID))
	if channelID == "" || userID == "" {
		return "", time.Time{}, errors.New("signedurl: channel and user are required")
	}
	if !strings.HasPrefix(strings.ToLower(u.Path), "/stream/channel/"+channelID+"/") {
		return "", time.Time{}, fmt.Errorf("signedurl: %s is not channel %s's URL", u.Path, channelID)
	}
	maxTTL := m.MaxTTL
	if maxTTL <= 0 {
		maxTTL = DefaultMaxTTL
	}
	if ttl <= 0 {
		ttl = DefaultChannelTTL
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	now := time.Now().UTC()
	if m.Now != nil {
		now = m.Now().UTC()
	}
	exp := now.Add(ttl)
	tok, err := jwt.Sign(m.set, jwt.Claims{
		Iss: "maktaba", Aud: AudChannel, Sub: channelID, Iat: now.Unix(), Exp: exp.Unix(), Usr: userID, IsAdmin: admin,
		Fam: family, Ac: ceiling,
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: %v", ErrKeyUnavailable, err)
	}
	q := u.Query()
	q.Set("sig", tok)
	u.RawQuery = q.Encode()
	return u.String(), time.Unix(exp.Unix(), 0).UTC(), nil
}
