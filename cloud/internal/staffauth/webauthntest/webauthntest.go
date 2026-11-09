// Package webauthntest is a virtual WebAuthn authenticator for tests (story 50.33 T05): it
// answers a registration with an attestation of format "none" and an assertion with an
// ES256 signature, as a platform passkey would, and lets a test break one rule at a time
// (another rpId, no user verification, a counter that goes backwards).
package webauthntest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"

	"github.com/fxamacker/cbor/v2"
	"github.com/go-webauthn/webauthn/protocol"
)

// Authenticator holds one passkey.
type Authenticator struct {
	// Origin is the page the browser says the ceremony ran on.
	Origin string
	// RPID overrides the relying party the authenticator signs for ("" is the options' own).
	RPID string
	// NoUV leaves the user-verified flag off.
	NoUV bool
	// Counter is the signature counter: a registration reports it, and each assertion moves
	// it on by one first and reports the new value.
	Counter uint32

	key        *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	rpID       string
}

// New is an authenticator with a fresh key, for pages on origin.
func New(origin string) *Authenticator {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &Authenticator{Origin: origin, key: key, credID: id}
}

// CredentialID is the passkey's id.
func (a *Authenticator) CredentialID() []byte { return a.credID }

var b64 = base64.RawURLEncoding

const (
	flagUP = 0x01
	flagUV = 0x04
	flagAT = 0x40
)

func (a *Authenticator) clientData(typ string, challenge []byte) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": b64.EncodeToString(challenge),
		"origin": a.Origin, "crossOrigin": false})
	return b
}

func (a *Authenticator) authData(rpID string, attested []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	flags := byte(flagUP)
	if !a.NoUV {
		flags |= flagUV
	}
	if attested != nil {
		flags |= flagAT
	}
	out := append([]byte{}, h[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, a.Counter)
	return append(out, attested...)
}

func (a *Authenticator) target(optionsRP string) string {
	if a.RPID != "" {
		return a.RPID
	}
	return optionsRP
}

// Register answers a registration's options: the JSON a browser posts back from
// navigator.credentials.create.
func (a *Authenticator) Register(opts *protocol.CredentialCreation) ([]byte, error) {
	if opts == nil {
		return nil, errors.New("webauthntest: no options")
	}
	pk := opts.Response
	a.rpID, a.userHandle = a.target(pk.RelyingParty.ID), userHandle(pk.User.ID)
	x, y := a.key.PublicKey.X.FillBytes(make([]byte, 32)), a.key.PublicKey.Y.FillBytes(make([]byte, 32))
	cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	if err != nil {
		return nil, err
	}
	attested := make([]byte, 16) // AAGUID: none
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(a.credID)))
	attested = append(attested, a.credID...)
	attested = append(attested, cose...)
	obj, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(a.rpID, attested)})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(a.clientData("webauthn.create", pk.Challenge)),
			"attestationObject": b64.EncodeToString(obj),
			"transports":        []string{"internal"},
		},
	})
}

// Assert answers an assertion's options: the JSON a browser posts back from
// navigator.credentials.get, signed with the counter moved on by one.
func (a *Authenticator) Assert(opts *protocol.CredentialAssertion) ([]byte, error) {
	if opts == nil {
		return nil, errors.New("webauthntest: no options")
	}
	pk := opts.Response
	a.Counter++
	cd := a.clientData("webauthn.get", pk.Challenge)
	ad := a.authData(a.target(pk.RelyingPartyID), nil)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(ad),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
	})
}

// userHandle reads the options' user id: bytes when the test passes the server's own value,
// base64url when it went through JSON.
func userHandle(id any) []byte {
	switch v := id.(type) {
	case []byte:
		return v
	case protocol.URLEncodedBase64:
		return v
	case string:
		b, _ := b64.DecodeString(v)
		return b
	}
	return nil
}
