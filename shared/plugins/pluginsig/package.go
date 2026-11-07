package pluginsig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// DeveloperSigned is developer.sig's signed object (§2).
type DeveloperSigned struct {
	PluginID    string `json:"plugin_id"`
	Version     string `json:"version"`
	FilesSHA256 string `json:"files_sha256"`
}

// StoreSigned is store.sig's signed object (§2). It names the developer's key, so the
// store's mark cannot be moved to a package another key signed.
type StoreSigned struct {
	PluginID           string `json:"plugin_id"`
	Version            string `json:"version"`
	FilesSHA256        string `json:"files_sha256"`
	DeveloperKeyID     string `json:"developer_key_id"`
	ReviewLevel        string `json:"review_level"`
	ReviewedAt         string `json:"reviewed_at"`
	CapabilitiesSHA256 string `json:"capabilities_sha256"`
}

// SigFile is a package signature file: {key_id, sig, signed}. A developer's also carries
// its public key (its id is the key's fingerprint, so the file proves which key signed);
// the store's key is in store-keys.json.
type SigFile struct {
	KeyID  string          `json:"key_id"`
	Public string          `json:"public,omitempty"`
	Sig    string          `json:"sig"`
	Signed json.RawMessage `json:"signed"`
}

// FileEntry is one line of files.json.
type FileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// FilesSHA256 is the sha256 of the canonical bytes of a files.json: what both signatures
// name (files_sha256).
func FilesSHA256(filesJSON []byte) (string, error) {
	c, err := Canonical(filesJSON)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return hex.EncodeToString(sum[:]), nil
}

// CapabilitiesSHA256 is the sha256 of the canonical bytes of a normalised manifest's
// capabilities object ({"optional":[…],"required":[…]}): what store.sig names.
func CapabilitiesSHA256(capabilities any) (string, error) {
	c, err := CanonicalOf(capabilities)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return hex.EncodeToString(sum[:]), nil
}

// Package is what a home verifies of a plugin package.
type Package struct {
	FilesJSON    []byte
	DeveloperSig []byte
	StoreSig     []byte
	// PluginID, Version and CapabilitiesSHA256 are the package's manifest's.
	PluginID           string
	Version            string
	CapabilitiesSHA256 string
	// Open reads a file of the package by its files.json path, for the last step (every
	// file's sha256 and size). nil skips that step: the caller unpacked the package with
	// componentpack.UnpackPlugin, which checks the same digests against the same files.json.
	Open func(path string) (io.ReadCloser, error)
}

// Result is a verified package.
type Result struct {
	Level          string
	DeveloperKeyID string
	ReviewedAt     time.Time
	FilesSHA256    string
	// StoreKeyID is the key that countersigned ("" below automated).
	StoreKeyID string
}

func parseSigFile(raw []byte, step string) (*SigFile, error) {
	if _, err := parseJSON(raw); err != nil {
		return nil, fail(CodeSignatureInvalid, step, "%v", err)
	}
	var f SigFile
	if err := strictUnmarshal(raw, &f); err != nil {
		return nil, fail(CodeSignatureInvalid, step, "%v", err)
	}
	if len(f.Signed) == 0 {
		return nil, fail(CodeSignatureInvalid, step, "no signed object")
	}
	return &f, nil
}

// VerifyPackage runs the chain of §3 from store.sig on (keys is the verified
// store-keys.json; nil when the home has none): store.sig (key listed for use store, valid
// at reviewed_at) → developer.sig (the key store.sig names) → every file's sha256 and size.
// A package with no signature is level none and one with developer.sig alone is level
// developer; any signature present must verify, or the package is refused.
func VerifyPackage(p Package, keys *StoreKeys) (*Result, error) {
	filesSum, err := FilesSHA256(p.FilesJSON)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepDigest, "files.json: %v", err)
	}
	res := &Result{Level: LevelNone, FilesSHA256: filesSum}
	var store *StoreSigned
	if len(p.StoreSig) > 0 {
		if keys == nil {
			return nil, fail(CodeSignatureInvalid, StepStoreKeys, "the home has no store-keys.json to check store.sig with")
		}
		f, err := parseSigFile(p.StoreSig, StepStoreSig)
		if err != nil {
			return nil, err
		}
		if f.Public != "" {
			return nil, fail(CodeSignatureInvalid, StepStoreSig, "store.sig carries a public key; the store's keys come from store-keys.json")
		}
		var s StoreSigned
		if err := strictUnmarshal(f.Signed, &s); err != nil {
			return nil, fail(CodeSignatureInvalid, StepStoreSig, "%v", err)
		}
		at, err := parseTime("reviewed_at", s.ReviewedAt)
		if err != nil {
			return nil, fail(CodeSignatureInvalid, StepStoreSig, "%v", err)
		}
		env := &Envelope{Signed: f.Signed, Signatures: []Signature{{KeyID: f.KeyID, Sig: f.Sig}}}
		if err := keys.verifyEnvelope(env, KindStore, UseStore, StepStoreSig, at); err != nil {
			return nil, err
		}
		switch {
		case s.ReviewLevel != LevelAutomated && s.ReviewLevel != LevelReviewed:
			return nil, fail(CodeSignatureInvalid, StepStoreSig, "review level %q", s.ReviewLevel)
		case s.FilesSHA256 != filesSum:
			return nil, fail(CodeSignatureInvalid, StepStoreSig, "store.sig covers files.json %s, the package's is %s", s.FilesSHA256, filesSum)
		case s.PluginID != p.PluginID || s.Version != p.Version:
			return nil, fail(CodeSignatureInvalid, StepStoreSig, "store.sig is for %s %s, the manifest is %s %s", s.PluginID, s.Version, p.PluginID, p.Version)
		case s.CapabilitiesSHA256 != p.CapabilitiesSHA256:
			return nil, fail(CodeSignatureInvalid, StepStoreSig, "the capabilities differ from those the store reviewed")
		}
		store = &s
		res.Level, res.ReviewedAt, res.StoreKeyID = s.ReviewLevel, at, f.KeyID
	}
	if len(p.DeveloperSig) == 0 {
		if store != nil {
			return nil, fail(CodeSignatureInvalid, StepDeveloperSig, "store.sig names developer key %s and the package has no developer.sig", store.DeveloperKeyID)
		}
	} else {
		f, err := parseSigFile(p.DeveloperSig, StepDeveloperSig)
		if err != nil {
			return nil, err
		}
		if store != nil && f.KeyID != store.DeveloperKeyID {
			return nil, fail(CodeDeveloperKeyMismatch, StepDeveloperSig, "signed by %s, store.sig names %s", f.KeyID, store.DeveloperKeyID)
		}
		pub, err := decodeKey(f.KeyID, f.Public)
		if err != nil {
			return nil, fail(CodeSignatureInvalid, StepDeveloperSig, "%v", err)
		}
		if !verifySig(pub, KindDeveloper, f.Signed, f.Sig) {
			return nil, fail(CodeSignatureInvalid, StepDeveloperSig, "the signature by %s does not verify", f.KeyID)
		}
		var d DeveloperSigned
		if err := strictUnmarshal(f.Signed, &d); err != nil {
			return nil, fail(CodeSignatureInvalid, StepDeveloperSig, "%v", err)
		}
		switch {
		case d.FilesSHA256 != filesSum:
			return nil, fail(CodeSignatureInvalid, StepDeveloperSig, "developer.sig covers files.json %s, the package's is %s", d.FilesSHA256, filesSum)
		case d.PluginID != p.PluginID || d.Version != p.Version:
			return nil, fail(CodeSignatureInvalid, StepDeveloperSig, "developer.sig is for %s %s, the manifest is %s %s", d.PluginID, d.Version, p.PluginID, p.Version)
		}
		res.DeveloperKeyID = f.KeyID
		if store == nil {
			res.Level = LevelDeveloper
		}
	}
	if p.Open != nil {
		if err := checkFiles(p.FilesJSON, p.Open); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// checkFiles is the last step: every file of files.json has its size and sha256.
func checkFiles(filesJSON []byte, open func(string) (io.ReadCloser, error)) error {
	var list []FileEntry
	if err := strictUnmarshal(filesJSON, &list); err != nil {
		return fail(CodeSignatureInvalid, StepDigest, "files.json: %v", err)
	}
	for _, f := range list {
		r, err := open(f.Path)
		if err != nil {
			return fail(CodeSignatureInvalid, StepDigest, "%s: %v", f.Path, err)
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(r, f.Size+1))
		_ = r.Close()
		if err != nil {
			return fail(CodeSignatureInvalid, StepDigest, "%s: %v", f.Path, err)
		}
		if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return fail(CodeSignatureInvalid, StepDigest, "%s does not match files.json", f.Path)
		}
	}
	return nil
}

// SignDeveloper makes a developer.sig for files.json (the SDK's and the tests').
func SignDeveloper(s Signer, pluginID, version string, filesJSON []byte) ([]byte, error) {
	sum, err := FilesSHA256(filesJSON)
	if err != nil {
		return nil, err
	}
	return signFile(s, KindDeveloper, true, DeveloperSigned{PluginID: pluginID, Version: version, FilesSHA256: sum})
}

// SignStore makes a store.sig (the store signs in TypeScript; Go's is for tests and drills).
func SignStore(s Signer, signed StoreSigned) ([]byte, error) {
	return signFile(s, KindStore, false, signed)
}

func signFile(s Signer, kind string, withPublic bool, signed any) ([]byte, error) {
	c, err := CanonicalOf(signed)
	if err != nil {
		return nil, err
	}
	sig, err := s.Sign(kind, c)
	if err != nil {
		return nil, err
	}
	f := SigFile{KeyID: s.ID, Sig: sig, Signed: c}
	if withPublic {
		f.Public = s.Public()
	}
	return CanonicalOf(f)
}

// VerifyEntitlement checks a signed entitlement envelope (Phase 3, 54.19 §6): signed by a
// key of use entitlement in store-keys.json, valid now. Its content is 54.19's; this
// answers the signed object.
func VerifyEntitlement(raw []byte, keys *StoreKeys, st State, clock time.Time) (json.RawMessage, error) {
	if keys == nil {
		return nil, fail(CodeEntitlementInvalid, StepStoreKeys, "no store-keys.json")
	}
	env, err := parseEnvelope(raw)
	if err != nil {
		return nil, fail(CodeEntitlementInvalid, StepList, "%v", err)
	}
	if err := keys.verifyEnvelope(env, KindEntitlement, UseEntitlement, StepList, st.Now(clock)); err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == CodeKeyUseMismatch {
			return nil, err
		}
		return nil, fail(CodeEntitlementInvalid, StepList, "%v", err)
	}
	return env.Signed, nil
}
