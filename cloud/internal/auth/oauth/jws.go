package oauth

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"time"
)

// signES256 produces the Apple-style client_secret JWT. Format:
//   base64url(JSON header).base64url(JSON payload).base64url(ECDSA r||s)
// We avoid jwt-go because the input space is tiny and constant.
func signES256(key *ecdsa.PrivateKey, teamID, keyID, clientID string, now time.Time) (string, error) {
	header := map[string]string{"alg": "ES256", "kid": keyID, "typ": "JWT"}
	payload := map[string]any{
		"iss": teamID,
		"iat": now.Unix(),
		"exp": now.Add(20 * time.Minute).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": clientID,
	}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	signingInput := jwtURLEncode(hb) + "." + jwtURLEncode(pb)
	h := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, h[:])
	if err != nil {
		return "", err
	}
	// P-256 → 32 bytes per coordinate, big-endian, fixed width.
	sig := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):], sBytes)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func jwtURLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func jwtURLDecode(seg string) ([]byte, error) {
	if i := strings.IndexByte(seg, '.'); i >= 0 {
		seg = seg[:i]
	}
	return base64.RawURLEncoding.DecodeString(seg)
}

// bigFromBytes is a helper kept for symmetry — not used today but is
// the natural decoder when we add JWKS verification.
func bigFromBytes(b []byte) *big.Int { return new(big.Int).SetBytes(b) }

// SignAPNsToken produces the JWT used in the APNs `authorization`
// header. APNs requires the same ES256+kid+iss(teamID) shape as the
// Apple OAuth client_secret, with a different audience.
func SignAPNsToken(key *ecdsa.PrivateKey, teamID, keyID string, now time.Time) (string, error) {
	header := map[string]string{"alg": "ES256", "kid": keyID, "typ": "JWT"}
	payload := map[string]any{
		"iss": teamID,
		"iat": now.Unix(),
	}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	signingInput := jwtURLEncode(hb) + "." + jwtURLEncode(pb)
	h := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, h[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):], sBytes)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
