package subscriptions

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"time"
)

// RevocationList is the license server's signed list of revoked license
// ids (story 16.4), fetched daily. The home keeps the last one it
// verified and uses it while the server is unreachable.
type RevocationList struct {
	Revocations RevocationsInner `json:"revocations"`
	Signature   string           `json:"signature"`
}

// RevocationsInner is the signed payload, in this field order (see
// canonicalJSON).
type RevocationsInner struct {
	IssuedAt   time.Time `json:"issued_at"`
	LicenseIDs []string  `json:"license_ids"`
}

// VerifyRevocations checks the list's signature with the license key.
func (v *Verifier) VerifyRevocations(list *RevocationList) error {
	if list == nil {
		return errors.New("nil revocation list")
	}
	if v == nil || v.PublicKey == nil {
		return errors.New("verifier has no public key")
	}
	sig, err := base64.StdEncoding.DecodeString(list.Signature)
	if err != nil {
		return ErrBadSignature
	}
	payload, err := canonicalJSON(list.Revocations)
	if err != nil {
		return err
	}
	if !ed25519.Verify(v.PublicKey, payload, sig) {
		return ErrBadSignature
	}
	return nil
}

// SignRevocations is the test helper's counterpart of the cloud signer.
func SignRevocations(priv ed25519.PrivateKey, inner RevocationsInner) (*RevocationList, error) {
	if inner.LicenseIDs == nil {
		inner.LicenseIDs = []string{}
	}
	payload, err := canonicalJSON(inner)
	if err != nil {
		return nil, err
	}
	return &RevocationList{Revocations: inner, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))}, nil
}
