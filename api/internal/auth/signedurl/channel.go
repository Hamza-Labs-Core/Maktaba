package signedurl

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
)

// AudChannel is the audience of a signed live channel URL (Epic 57, story 57.10):
//
//	/stream/channel/{channel_id}/…   aud=streaming-channel   sub=channel_id   usr=user_id
//
// A channel may have no library, so the token carries no lib claim: the streaming
// service checks that the subject is the channel in the path, and asks the API
// whether usr may watch what the channel plays.
const AudChannel = "streaming-channel"

// DefaultChannelTTL is how long a signed channel URL lasts: a player asks for a new
// one before it runs out.
const DefaultChannelTTL = 6 * time.Hour

// SignChannelURL signs a channel's stream URL for userID. ttl is clamped to MaxTTL
// (0: DefaultChannelTTL). Returns the signed URL and its expiry.
func (m *Minter) SignChannelURL(rawURL, channelID, userID string, ttl time.Duration) (string, time.Time, error) {
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
		Iss: "maktaba", Aud: AudChannel, Sub: channelID, Iat: now.Unix(), Exp: exp.Unix(), Usr: userID,
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: %v", ErrKeyUnavailable, err)
	}
	q := u.Query()
	q.Set("sig", tok)
	u.RawQuery = q.Encode()
	return u.String(), time.Unix(exp.Unix(), 0).UTC(), nil
}
