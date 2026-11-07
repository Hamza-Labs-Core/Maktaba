// Package pluginsig is the home's half of the plugin chain of trust (Epic 54, story 54.14):
// the embedded root (plugins-root.json), the store's online keys (store-keys.json), the two
// package signatures over files.json (developer.sig, store.sig), the signed lists (the
// revocation list and its head, the catalog) and the newest versions a home has seen.
//
// It is separate from Epic 51's release verification (shared/go/releaseverify): nothing here
// reads the component index or the release's Sigstore identity (README H9). The same rules
// are in Rust (plugind/crates/sig) and the store signs in TypeScript (store/signing); the
// three agree on shared/plugins/testdata/signatures/.
//
// Every signature is Ed25519 over a message that names what it signs:
//
//	"maktaba-plugins/v1/" + kind + "\n" + canonical JSON of the signed object
//
// so a signature made for one kind of file (a catalog) is never valid for another (a
// revocation list), whichever key made it. Key ids are the first 16 bytes of the sha256 of
// the raw public key, in hex: an id names exactly one key.
package pluginsig

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Kinds of signed object (the message's kind).
const (
	KindRoot        = "root"
	KindStoreKeys   = "store-keys"
	KindDeveloper   = "developer"
	KindStore       = "store"
	KindCatalog     = "catalog"
	KindRevoked     = "revoked"
	KindRevokedHead = "revoked-head"
	KindEntitlement = "entitlement"
)

// Key uses in store-keys.json (§1): a key is valid only for its use.
const (
	UseStore       = "store"
	UseEntitlement = "entitlement"
)

// Review levels a home records (§2).
const (
	LevelNone      = "none"
	LevelDeveloper = "developer"
	LevelAutomated = "automated"
	LevelReviewed  = "reviewed"
)

// Codes of a refusal (words in web/src/i18n as plugins.error.<code>).
const (
	CodeSignatureInvalid     = "plugin_signature_invalid"
	CodeDeveloperKeyMismatch = "developer_key_mismatch"
	CodeKeyUseMismatch       = "key_use_mismatch"
	CodeStoreKeysRollback    = "store_keys_rollback_refused"
	CodeStoreKeysExpired     = "store_keys_expired"
	CodeRevocationRollback   = "revocation_rollback_refused"
	CodeRevocationExpired    = "revocation_expired"
	CodeRevocationHead       = "revocation_head_mismatch"
	CodeCatalogRollback      = "catalog_rollback_refused"
	CodeCatalogExpired       = "catalog_expired"
	CodeEntitlementInvalid   = "entitlement_invalid"
)

// Steps of the chain (§3), named in a refusal.
const (
	StepRoot         = "root"
	StepStoreKeys    = "store_keys"
	StepStoreSig     = "store_sig"
	StepDeveloperSig = "developer_sig"
	StepDigest       = "digest"
	StepList         = "list"
)

// Error is a refusal: its code, the step of the chain that refused, and why.
type Error struct {
	Code string
	Step string
	Msg  string
}

func (e *Error) Error() string {
	if e.Step == "" {
		return e.Code + ": " + e.Msg
	}
	return e.Code + ": " + e.Step + ": " + e.Msg
}

func fail(code, step, format string, a ...any) *Error {
	return &Error{Code: code, Step: step, Msg: fmt.Sprintf(format, a...)}
}

// CodeOf answers a refusal's code and step ("" for another error).
func CodeOf(err error) (code, step string) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, e.Step
	}
	return "", ""
}

// KeyID is the id of a raw Ed25519 public key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:16])
}

// b64 is standard base64 with padding, as every file here writes keys and signatures.
var b64 = base64.StdEncoding

// decodeKey reads a base64 public key and checks that id names it.
func decodeKey(id, public string) (ed25519.PublicKey, error) {
	raw, err := b64.DecodeString(public)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("key %s: not an Ed25519 public key", id)
	}
	if KeyID(raw) != id {
		return nil, fmt.Errorf("key %s: the id does not name its public key", id)
	}
	return ed25519.PublicKey(raw), nil
}

// Message is what a signature of kind covers: the kind's prefix and the canonical
// bytes of signed (a JSON text).
func Message(kind string, signed []byte) ([]byte, error) {
	c, err := Canonical(signed)
	if err != nil {
		return nil, err
	}
	return append([]byte("maktaba-plugins/v1/"+kind+"\n"), c...), nil
}

func verifySig(pub ed25519.PublicKey, kind string, signed []byte, sig string) bool {
	s, err := b64.DecodeString(sig)
	if err != nil || len(s) != ed25519.SignatureSize {
		return false
	}
	msg, err := Message(kind, signed)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, msg, s)
}

// Signature is one signature of an envelope.
type Signature struct {
	KeyID string `json:"key_id"`
	Sig   string `json:"sig"`
}

// Envelope is a signed list: the signed object and its signatures.
type Envelope struct {
	Signed     json.RawMessage `json:"signed"`
	Signatures []Signature     `json:"signatures"`
}

func parseEnvelope(raw []byte) (*Envelope, error) {
	if _, err := parseJSON(raw); err != nil {
		return nil, err
	}
	var env Envelope
	if err := strictUnmarshal(raw, &env); err != nil {
		return nil, err
	}
	if len(env.Signed) == 0 {
		return nil, errors.New("no signed object")
	}
	return &env, nil
}

// strictUnmarshal decodes raw into v with unknown fields refused.
func strictUnmarshal(raw []byte, v any) error {
	dec := json.NewDecoder(bytesReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// countValid counts the distinct keys of keys (id → key) with a valid signature in env.
func countValid(env *Envelope, kind string, keys map[string]ed25519.PublicKey) int {
	seen := map[string]bool{}
	for _, s := range env.Signatures {
		pub, ok := keys[s.KeyID]
		if !ok || seen[s.KeyID] {
			continue
		}
		if verifySig(pub, kind, env.Signed, s.Sig) {
			seen[s.KeyID] = true
		}
	}
	return len(seen)
}

// ---- signing (the store's and the tests'; a home never signs) ---------------------------

// Signer is a private key and its id.
type Signer struct {
	ID  string
	Key ed25519.PrivateKey
}

// NewSigner makes a Signer from a 32-byte Ed25519 seed.
func NewSigner(seed []byte) Signer {
	k := ed25519.NewKeyFromSeed(seed)
	return Signer{ID: KeyID(k.Public().(ed25519.PublicKey)), Key: k}
}

// Public is the signer's public key in base64.
func (s Signer) Public() string { return b64.EncodeToString(s.Key.Public().(ed25519.PublicKey)) }

// Sign signs the JSON text signed as kind.
func (s Signer) Sign(kind string, signed []byte) (string, error) {
	msg, err := Message(kind, signed)
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(ed25519.Sign(s.Key, msg)), nil
}

// SignEnvelope makes an envelope of signed (any JSON value) signed by each of signers.
func SignEnvelope(kind string, signed any, signers ...Signer) ([]byte, error) {
	c, err := CanonicalOf(signed)
	if err != nil {
		return nil, err
	}
	env := Envelope{Signed: c, Signatures: []Signature{}}
	for _, s := range signers {
		sig, err := s.Sign(kind, c)
		if err != nil {
			return nil, err
		}
		env.Signatures = append(env.Signatures, Signature{KeyID: s.ID, Sig: sig})
	}
	return CanonicalOf(env)
}

// ---- times -------------------------------------------------------------------------------

// parseTime reads an RFC 3339 time in UTC ("2026-11-06T12:00:00Z").
func parseTime(field, s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %q is not an RFC 3339 time", field, s)
	}
	return t.UTC(), nil
}

// FormatTime writes t as the files do.
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }
