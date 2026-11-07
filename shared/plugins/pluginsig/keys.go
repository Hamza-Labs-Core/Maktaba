package pluginsig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// ---- the root (§1) -----------------------------------------------------------------------

// RootKey is one of the root's keys.
type RootKey struct {
	ID     string `json:"id"`
	Public string `json:"public"`
}

// RootSigned is plugins-root.json's signed object.
type RootSigned struct {
	Version   int64     `json:"version"`
	Threshold int       `json:"threshold"`
	Keys      []RootKey `json:"keys"`
}

// Root is a parsed root: its version, threshold and keys.
type Root struct {
	Version   int64
	Threshold int
	keys      map[string]ed25519.PublicKey
	raw       []byte
}

// KeyIDs are the root's key ids.
func (r *Root) KeyIDs() []string {
	ids := make([]string, 0, len(r.keys))
	for id := range r.keys {
		ids = append(ids, id)
	}
	return ids
}

// Empty reports a placeholder root with no keys (a build before the owner made them):
// nothing a store signed verifies under it.
func (r *Root) Empty() bool { return len(r.keys) == 0 }

// ParseRoot reads a plugins-root.json. An embedded root is trusted as it is (it is part of
// the binary); its own signatures matter only for chaining (VerifyRootUpdate).
func ParseRoot(raw []byte) (*Root, error) {
	env, err := parseEnvelope(raw)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepRoot, "%v", err)
	}
	var s RootSigned
	if err := strictUnmarshal(env.Signed, &s); err != nil {
		return nil, fail(CodeSignatureInvalid, StepRoot, "%v", err)
	}
	r := &Root{Version: s.Version, Threshold: s.Threshold, keys: map[string]ed25519.PublicKey{}, raw: raw}
	for _, k := range s.Keys {
		pub, err := decodeKey(k.ID, k.Public)
		if err != nil {
			return nil, fail(CodeSignatureInvalid, StepRoot, "%v", err)
		}
		r.keys[k.ID] = pub
	}
	if len(r.keys) > 0 && (r.Threshold < 1 || r.Threshold > len(r.keys)) {
		return nil, fail(CodeSignatureInvalid, StepRoot, "threshold %d with %d keys", r.Threshold, len(r.keys))
	}
	if len(r.keys) > 0 && countValid(env, KindRoot, r.keys) < r.Threshold {
		return nil, fail(CodeSignatureInvalid, StepRoot, "the root is not signed by its own keys")
	}
	return r, nil
}

// VerifyRootUpdate checks a new root against the one before it (TUF's root chaining, and
// nothing more): the next version, signed by the old root's threshold and its own.
func VerifyRootUpdate(old *Root, next []byte) (*Root, error) {
	r, err := ParseRoot(next)
	if err != nil {
		return nil, err
	}
	env, _ := parseEnvelope(next)
	if r.Version != old.Version+1 {
		return nil, fail(CodeSignatureInvalid, StepRoot, "version %d does not follow %d", r.Version, old.Version)
	}
	if countValid(env, KindRoot, old.keys) < old.Threshold {
		return nil, fail(CodeSignatureInvalid, StepRoot, "version %d is not signed by version %d's keys", r.Version, old.Version)
	}
	return r, nil
}

// ---- store-keys.json (§1) ----------------------------------------------------------------

// StoreKey is one online key.
type StoreKey struct {
	ID        string `json:"id"`
	Public    string `json:"public"`
	Use       string `json:"use"`
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`
}

// StoreKeysSigned is store-keys.json's signed object.
type StoreKeysSigned struct {
	Version int64      `json:"version"`
	Expires string     `json:"expires"`
	Keys    []StoreKey `json:"keys"`
}

type storeKey struct {
	pub       ed25519.PublicKey
	use       string
	notBefore time.Time
	notAfter  time.Time
}

// StoreKeys is a verified store-keys.json.
type StoreKeys struct {
	Version int64
	Expires time.Time
	// SHA256 is the sha256 of the file's canonical signed object.
	SHA256 string
	keys   map[string]storeKey
	// Raw is the file as fetched.
	Raw []byte
}

// Has reports whether id is listed (for any use).
func (k *StoreKeys) Has(id string) bool { _, ok := k.keys[id]; return ok }

// VerifyStoreKeys checks a store-keys.json (§3, the second step): signed by the root's
// threshold, not expired at the home's time (State.Now), and not older than the newest
// the home has seen (anti-rollback). The same version with other content is refused too.
func (r *Root) VerifyStoreKeys(raw []byte, st State, clock time.Time) (*StoreKeys, error) {
	if r == nil || r.Empty() {
		return nil, fail(CodeSignatureInvalid, StepRoot, "this build carries no plugin root keys")
	}
	env, err := parseEnvelope(raw)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepStoreKeys, "%v", err)
	}
	if countValid(env, KindStoreKeys, r.keys) < r.Threshold {
		return nil, fail(CodeSignatureInvalid, StepStoreKeys, "not signed by a key of the plugin root")
	}
	var s StoreKeysSigned
	if err := strictUnmarshal(env.Signed, &s); err != nil {
		return nil, fail(CodeSignatureInvalid, StepStoreKeys, "%v", err)
	}
	exp, err := parseTime("expires", s.Expires)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepStoreKeys, "%v", err)
	}
	c, _ := Canonical(env.Signed)
	sum := sha256.Sum256(c)
	out := &StoreKeys{Version: s.Version, Expires: exp, SHA256: hex.EncodeToString(sum[:]), keys: map[string]storeKey{}, Raw: raw}
	if s.Version < st.StoreKeysVersion {
		return nil, fail(CodeStoreKeysRollback, StepStoreKeys, "version %d is older than %d, the newest seen", s.Version, st.StoreKeysVersion)
	}
	if s.Version == st.StoreKeysVersion && st.StoreKeysSHA256 != "" && st.StoreKeysSHA256 != out.SHA256 {
		return nil, fail(CodeStoreKeysRollback, StepStoreKeys, "version %d differs from the copy seen before", s.Version)
	}
	if now := st.Now(clock); now.After(exp) {
		return nil, fail(CodeStoreKeysExpired, StepStoreKeys, "expired %s", s.Expires)
	}
	for _, k := range s.Keys {
		pub, err := decodeKey(k.ID, k.Public)
		if err != nil {
			return nil, fail(CodeSignatureInvalid, StepStoreKeys, "%v", err)
		}
		if k.Use != UseStore && k.Use != UseEntitlement {
			return nil, fail(CodeSignatureInvalid, StepStoreKeys, "key %s: use %q", k.ID, k.Use)
		}
		nb, err := parseTime("not_before", k.NotBefore)
		if err != nil {
			return nil, fail(CodeSignatureInvalid, StepStoreKeys, "%v", err)
		}
		na, err := parseTime("not_after", k.NotAfter)
		if err != nil {
			return nil, fail(CodeSignatureInvalid, StepStoreKeys, "%v", err)
		}
		if _, dup := out.keys[k.ID]; dup {
			return nil, fail(CodeSignatureInvalid, StepStoreKeys, "key %s is listed twice", k.ID)
		}
		out.keys[k.ID] = storeKey{pub: pub, use: k.Use, notBefore: nb, notAfter: na}
	}
	return out, nil
}

// keyFor is the key id of use, valid at t ("" t: any time in its window is not checked).
func (k *StoreKeys) keyFor(id, use, step string) (storeKey, error) {
	sk, ok := k.keys[id]
	if !ok {
		return storeKey{}, fail(CodeSignatureInvalid, step, "key %s is not in store-keys.json version %d", id, k.Version)
	}
	if sk.use != use {
		return storeKey{}, fail(CodeKeyUseMismatch, step, "key %s is a %s key, not a %s key", id, sk.use, use)
	}
	return sk, nil
}

// verifyEnvelope checks env is signed by a key of use valid at t.
func (k *StoreKeys) verifyEnvelope(env *Envelope, kind, use, step string, t time.Time) error {
	var lastErr error = fail(CodeSignatureInvalid, step, "no signature")
	for _, s := range env.Signatures {
		sk, err := k.keyFor(s.KeyID, use, step)
		if err != nil {
			lastErr = err
			continue
		}
		if t.Before(sk.notBefore) || t.After(sk.notAfter) {
			lastErr = fail(CodeSignatureInvalid, step, "key %s is not valid at %s", s.KeyID, FormatTime(t))
			continue
		}
		if verifySig(sk.pub, kind, env.Signed, s.Sig) {
			return nil
		}
		lastErr = fail(CodeSignatureInvalid, step, "the signature by %s does not verify", s.KeyID)
	}
	return lastErr
}

func (k *StoreKeys) String() string {
	return fmt.Sprintf("store-keys v%d (%d keys)", k.Version, len(k.keys))
}
