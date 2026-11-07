package plugins

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// Plugin entitlements, checked offline (Epic 54, story 54.19 §6). The store signs an
// entitlement with its entitlement key E (story 54.14: `use: "entitlement"` in
// store-keys.json, under the plugin root every server embeds); the home checks it here,
// without asking the store or Maktaba Cloud, and never learns which payment provider took
// the sale. The store keys come from 54.14's verified store-keys.json; this file checks one
// entitlement against them.

// Entitlement refusal codes.
const (
	CodeEntitlementBadSignature = "entitlement_bad_signature"
	CodeEntitlementUnknownKey   = "entitlement_unknown_key"
	CodeEntitlementKeyUse       = "key_use_mismatch"
	CodeEntitlementOtherHome    = "entitlement_other_home"
	CodeEntitlementExpired      = "entitlement_expired"
	CodeEntitlementMalformed    = "entitlement_malformed"
)

// EntitlementGrace is the clock skew allowed past valid_until (Epic 16's SkewGrace, the same
// number as the home's licences).
const EntitlementGrace = 72 * time.Hour

// StoreKey is one key of store-keys.json (54.14 §1).
type StoreKey struct {
	ID        string    `json:"id"`
	Public    string    `json:"public"`
	Use       string    `json:"use"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
}

// EntitlementBody is what the store signs (version 1).
type EntitlementBody struct {
	V          int       `json:"v"`
	HomeID     string    `json:"home_id"`
	HomeKeyFP  string    `json:"home_key_fp"`
	PluginID   string    `json:"plugin_id"`
	Product    string    `json:"product"`
	PurchaseID string    `json:"purchase_id"`
	ValidFrom  time.Time `json:"valid_from"`
	ValidUntil time.Time `json:"valid_until"`
	IssuedAt   time.Time `json:"issued_at"`
}

// SignedEntitlement is 54.14's signature file: the key id, the signature and the body.
type SignedEntitlement struct {
	KeyID  string          `json:"key_id"`
	Sig    string          `json:"sig"`
	Signed json.RawMessage `json:"signed"`
}

// EntitlementError says why an entitlement does not hold.
type EntitlementError struct{ Code string }

func (e *EntitlementError) Error() string { return e.Code }

func entErr(code string) error { return &EntitlementError{Code: code} }

// canonicalJSON is 54.14's canonical form: keys sorted, no spaces, as the store signs.
func canonicalJSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case json.Number:
		buf.WriteString(x.String())
	default:
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			return err
		}
		buf.Truncate(buf.Len() - 1) // Encode's newline
	}
	return nil
}

// VerifyEntitlement checks one entitlement offline: the key is an entitlement key of
// store-keys.json valid when it was issued, the signature holds over the canonical body, the
// body names this home and its store key, and now is within its validity (plus the grace).
func VerifyEntitlement(e SignedEntitlement, keys []StoreKey, homeID, homeKeyFP string, now time.Time) (*EntitlementBody, error) {
	var body EntitlementBody
	if err := json.Unmarshal(e.Signed, &body); err != nil || body.V != 1 {
		return nil, entErr(CodeEntitlementMalformed)
	}
	var key *StoreKey
	for i := range keys {
		k := &keys[i]
		if k.ID == e.KeyID && !body.IssuedAt.Before(k.NotBefore) && !body.IssuedAt.After(k.NotAfter) {
			key = k
			break
		}
	}
	if key == nil {
		return nil, entErr(CodeEntitlementUnknownKey)
	}
	if key.Use != "entitlement" {
		return nil, entErr(CodeEntitlementKeyUse)
	}
	pub, err := base64.StdEncoding.DecodeString(key.Public)
	sig, serr := base64.StdEncoding.DecodeString(e.Sig)
	msg, cerr := canonicalJSON(e.Signed)
	if err != nil || serr != nil || cerr != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, msg, sig) {
		return nil, entErr(CodeEntitlementBadSignature)
	}
	if body.HomeID != homeID || body.HomeKeyFP != homeKeyFP {
		return nil, entErr(CodeEntitlementOtherHome)
	}
	if now.Before(body.ValidFrom.Add(-EntitlementGrace)) || now.After(body.ValidUntil.Add(EntitlementGrace)) {
		return nil, entErr(CodeEntitlementExpired)
	}
	return &body, nil
}

// EntitledPlugins answers the plugin ids a set of entitlements covers now (the gate's
// entitlement_missing is a paid plugin outside it); entitlements that do not hold are left
// out with their codes.
func EntitledPlugins(es []SignedEntitlement, keys []StoreKey, homeID, homeKeyFP string, now time.Time) (map[string]bool, map[string]string) {
	ok := map[string]bool{}
	bad := map[string]string{}
	for _, e := range es {
		b, err := VerifyEntitlement(e, keys, homeID, homeKeyFP, now)
		if err != nil {
			var ee *EntitlementError
			code := CodeEntitlementMalformed
			if errors.As(err, &ee) {
				code = ee.Code
			}
			bad[e.KeyID+"/"+e.Sig] = code
			continue
		}
		ok[b.PluginID] = true
	}
	return ok, bad
}
