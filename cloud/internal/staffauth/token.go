package staffauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// Story 50.33 §2: the console's access token. HS256 under its own key
// (MAKTABA_CLOUD_STAFF_TOKEN_SECRET, never the customers'), aud admin.maktaba.tv, 10 minutes.
// A customer's token fails both the signature and the audience.

const (
	// AccessTTL is the staff access token's life.
	AccessTTL = 10 * time.Minute
	// Audience is the only audience a staff token has.
	Audience    = "admin.maktaba.tv"
	tokenIssuer = "https://admin.maktaba.tv"
)

// The amr values (RFC 8176): a password, and the second factor that came with it.
const (
	AMRPassword = "pwd"
	AMRPasskey  = "hwk"
	AMRTOTP     = "otp"
)

var (
	ErrTokenMalformed = errors.New("staffauth: token malformed")
	ErrTokenSignature = errors.New("staffauth: token signature")
	ErrTokenExpired   = errors.New("staffauth: token expired")
	ErrTokenAudience  = errors.New("staffauth: token audience")
)

// Claims is a staff token's payload.
type Claims struct {
	Iss string   `json:"iss"`
	Aud string   `json:"aud"`
	Sub string   `json:"sub"` // staff account id
	SID string   `json:"sid"` // staff session id
	AMR []string `json:"amr"`
	// AuthTime is the last MFA check (sign-in or re-authentication), unix seconds: §5's
	// five-minute window reads it.
	AuthTime int64  `json:"auth_time"`
	IAT      int64  `json:"iat"`
	EXP      int64  `json:"exp"`
	JTI      string `json:"jti"`
}

// HasMFA is §3 step 4: the token carries a second factor, not a password alone.
func (c Claims) HasMFA() bool {
	return slices.Contains(c.AMR, AMRPasskey) || slices.Contains(c.AMR, AMRTOTP)
}

// Signer issues and verifies staff tokens.
type Signer struct{ secret []byte }

// NewSigner takes the staff token key (at least 32 bytes; config checks it).
func NewSigner(secret []byte) *Signer { return &Signer{secret: secret} }

var tokenHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// Issue mints a token for staff id in session sid, with amr and the time of its MFA check.
func (s *Signer) Issue(id, sid string, amr []string, authTime, now time.Time) (string, error) {
	jti := make([]byte, 12)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	body, err := json.Marshal(Claims{
		Iss: tokenIssuer, Aud: Audience, Sub: id, SID: sid, AMR: amr, AuthTime: authTime.Unix(),
		IAT: now.Unix(), EXP: now.Add(AccessTTL).Unix(), JTI: hex.EncodeToString(jti),
	})
	if err != nil {
		return "", err
	}
	unsigned := tokenHeader + "." + base64.RawURLEncoding.EncodeToString(body)
	return unsigned + "." + s.sign(unsigned), nil
}

func (s *Signer) sign(unsigned string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(unsigned))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Verify checks the signature, the header, the issuer and audience and the expiry.
func (s *Signer) Verify(tok string, now time.Time) (Claims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] != tokenHeader {
		return Claims{}, ErrTokenMalformed
	}
	if !hmac.Equal([]byte(s.sign(parts[0]+"."+parts[1])), []byte(parts[2])) {
		return Claims{}, ErrTokenSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrTokenMalformed
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return Claims{}, ErrTokenMalformed
	}
	if c.Iss != tokenIssuer || c.Aud != Audience {
		return Claims{}, ErrTokenAudience
	}
	if c.Sub == "" || c.SID == "" {
		return Claims{}, ErrTokenMalformed
	}
	if now.Unix() >= c.EXP {
		return Claims{}, ErrTokenExpired
	}
	return c, nil
}
