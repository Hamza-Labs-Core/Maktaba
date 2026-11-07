package pluginsig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// The signed lists of §4 and 54.15 §8, each an envelope signed by a store key.

// Revocation actions (§4; applied by 54.17).
const (
	ActionDisable = "disable"
	ActionWarn    = "warn"
	ActionDelist  = "delist"
)

// Lifetimes a list may claim at most: a list that says it lives longer is refused.
const (
	RevokedLifetime = 48 * time.Hour
	CatalogLifetime = 7 * 24 * time.Hour
)

// RevocationEntry is one entry of revoked.json.
type RevocationEntry struct {
	PluginID       string `json:"plugin_id"`
	Versions       string `json:"versions"`
	DeveloperKeyID string `json:"developer_key_id,omitempty"`
	Action         string `json:"action"`
	ReasonCode     string `json:"reason_code"`
	Issued         string `json:"issued"`
}

// RevokedSigned is revoked.json's signed object.
type RevokedSigned struct {
	Version int64             `json:"version"`
	Issued  string            `json:"issued"`
	Expires string            `json:"expires"`
	Entries []RevocationEntry `json:"entries"`
}

// Revoked is a verified revocation list.
type Revoked struct {
	RevokedSigned
	IssuedAt  time.Time
	ExpiresAt time.Time
	// SHA256 is the sha256 of the file's bytes as served (what revoked-head.json names).
	SHA256 string
	Raw    []byte
}

// Expired reports whether the list is past its expiry at the home's time: it then warns
// and holds store installs, and never stops a plugin (§4, README H10).
func (r *Revoked) Expired(st State, clock time.Time) bool { return st.Now(clock).After(r.ExpiresAt) }

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// verifyList is the common part: an envelope signed by a store key valid at its issue time.
func (k *StoreKeys) verifyList(raw []byte, kind string, into any) (*Envelope, error) {
	if k == nil {
		return nil, fail(CodeSignatureInvalid, StepStoreKeys, "the home has no store-keys.json")
	}
	env, err := parseEnvelope(raw)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%s: %v", kind, err)
	}
	if err := strictUnmarshal(env.Signed, into); err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%s: %v", kind, err)
	}
	return env, nil
}

// VerifyRevoked checks a revoked.json: signed by a store key valid when it was issued, not
// older than the newest list seen, the same version only with the same bytes, issued no
// later than it expires, living at most 48 hours, and not already expired when received.
// An expired stored list is still used (Revoked.Expired); a newly received one is not.
func (k *StoreKeys) VerifyRevoked(raw []byte, st State, clock time.Time) (*Revoked, error) {
	var s RevokedSigned
	env, err := k.verifyList(raw, KindRevoked, &s)
	if err != nil {
		return nil, err
	}
	issued, err := parseTime("issued", s.Issued)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%v", err)
	}
	expires, err := parseTime("expires", s.Expires)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%v", err)
	}
	if err := k.verifyEnvelope(env, KindRevoked, UseStore, StepList, issued); err != nil {
		return nil, err
	}
	if !expires.After(issued) || expires.Sub(issued) > RevokedLifetime {
		return nil, fail(CodeSignatureInvalid, StepList, "revoked.json claims %s to %s", s.Issued, s.Expires)
	}
	for _, e := range s.Entries {
		if e.Action != ActionDisable && e.Action != ActionWarn && e.Action != ActionDelist {
			return nil, fail(CodeSignatureInvalid, StepList, "entry %s: action %q", e.PluginID, e.Action)
		}
		if e.PluginID == "" || e.Versions == "" {
			return nil, fail(CodeSignatureInvalid, StepList, "an entry names no plugin or versions")
		}
	}
	out := &Revoked{RevokedSigned: s, IssuedAt: issued, ExpiresAt: expires, SHA256: sha256Hex(raw), Raw: raw}
	if s.Version < st.RevokedVersion {
		return nil, fail(CodeRevocationRollback, StepList, "version %d is older than %d, the newest seen", s.Version, st.RevokedVersion)
	}
	if s.Version == st.RevokedVersion && st.RevokedSHA256 != "" && st.RevokedSHA256 != out.SHA256 {
		return nil, fail(CodeRevocationRollback, StepList, "version %d differs from the copy seen before", s.Version)
	}
	if st.Now(clock).After(expires) {
		return nil, fail(CodeRevocationExpired, StepList, "version %d expired %s", s.Version, s.Expires)
	}
	return out, nil
}

// LoadStoredRevoked re-verifies a list the home stored before (or a release embedded): the
// signature and form only. Its age is the caller's (Expired), and its version is the
// newest seen or older.
func (k *StoreKeys) LoadStoredRevoked(raw []byte) (*Revoked, error) {
	var s RevokedSigned
	env, err := k.verifyList(raw, KindRevoked, &s)
	if err != nil {
		return nil, err
	}
	issued, err := parseTime("issued", s.Issued)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%v", err)
	}
	expires, err := parseTime("expires", s.Expires)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%v", err)
	}
	if err := k.verifyEnvelope(env, KindRevoked, UseStore, StepList, issued); err != nil {
		return nil, err
	}
	return &Revoked{RevokedSigned: s, IssuedAt: issued, ExpiresAt: expires, SHA256: sha256Hex(raw), Raw: raw}, nil
}

// NewestRevoked picks the list to use of the stored one and the release's embedded one
// (§4): the higher version (either may be nil).
func NewestRevoked(stored, embedded *Revoked) *Revoked {
	switch {
	case stored == nil:
		return embedded
	case embedded == nil:
		return stored
	case embedded.Version > stored.Version:
		return embedded
	}
	return stored
}

// RevokedHeadSigned is revoked-head.json's signed object.
type RevokedHeadSigned struct {
	Version int64  `json:"version"`
	Issued  string `json:"issued"`
	SHA256  string `json:"sha256"`
}

// RevokedHead is a verified head.
type RevokedHead struct {
	RevokedHeadSigned
	IssuedAt time.Time
}

// VerifyRevokedHead checks a revoked-head.json. It only says a newer list exists; the list
// itself is still verified (§4). An older head than the list the home has is ignored by
// the caller (Newer), not refused, since the edge may serve a cached head for 60 s.
func (k *StoreKeys) VerifyRevokedHead(raw []byte) (*RevokedHead, error) {
	var s RevokedHeadSigned
	env, err := k.verifyList(raw, KindRevokedHead, &s)
	if err != nil {
		return nil, err
	}
	issued, err := parseTime("issued", s.Issued)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%v", err)
	}
	if err := k.verifyEnvelope(env, KindRevokedHead, UseStore, StepList, issued); err != nil {
		return nil, err
	}
	return &RevokedHead{RevokedHeadSigned: s, IssuedAt: issued}, nil
}

// Matches reports whether a fetched revoked.json is the one the head names (54.17:
// otherwise revocation_head_mismatch, the current list kept).
func (h *RevokedHead) Matches(r *Revoked) bool { return r.Version == h.Version && r.SHA256 == h.SHA256 }

// CatalogVersion is one version of a plugin in the catalog (54.15 §8).
type CatalogVersion struct {
	Version      string   `json:"version"`
	SHA256       string   `json:"sha256"`
	Size         int64    `json:"size"`
	URL          string   `json:"url"`
	Maktaba      string   `json:"maktaba"`
	PluginAPI    string   `json:"plugin_api"`
	Platforms    []string `json:"platforms"`
	Capabilities []string `json:"capabilities"`
	ReviewLevel  string   `json:"review_level"`
	Released     string   `json:"released"`
	// FilesSHA256 is the version's files.json digest (what its signatures name), so a home
	// can tell an installed copy is this version.
	FilesSHA256 string `json:"files_sha256"`
}

// CatalogPlugin is one plugin of the catalog.
type CatalogPlugin struct {
	ID          string            `json:"id"`
	Developer   string            `json:"developer"`
	Names       map[string]string `json:"names"`
	Summaries   map[string]string `json:"summaries"`
	Icon        string            `json:"icon"`
	Category    string            `json:"category"`
	Tags        []string          `json:"tags"`
	RatingAvg   int64             `json:"rating_avg_x100"`
	RatingCount int64             `json:"rating_count"`
	Installs    int64             `json:"installs"`
	Price       string            `json:"price"`
	// Machine names the languages the developer marked machine translated (54.16 §4).
	Machine  []string         `json:"machine_translated"`
	Versions []CatalogVersion `json:"versions"`
	// Extra holds the listing's other words (descriptions), carried and not checked.
	Extra json.RawMessage `json:"extra,omitempty"`
}

// CatalogSigned is catalog-<api major>.json's signed object.
type CatalogSigned struct {
	Version  int64           `json:"version"`
	Issued   string          `json:"issued"`
	Expires  string          `json:"expires"`
	APIMajor int64           `json:"api_major"`
	Plugins  []CatalogPlugin `json:"plugins"`
}

// Catalog is a verified catalog.
type Catalog struct {
	CatalogSigned
	IssuedAt  time.Time
	ExpiresAt time.Time
	SHA256    string
	Raw       []byte
}

// Expired: Browse shows it with a banner and Install waits (54.18).
func (c *Catalog) Expired(st State, clock time.Time) bool { return st.Now(clock).After(c.ExpiresAt) }

// VerifyCatalog checks a catalog for the plugin API major apiMajor, by the rules of a
// revocation list (newest seen, at most 7 days, not expired when received).
func (k *StoreKeys) VerifyCatalog(raw []byte, apiMajor int64, st State, clock time.Time) (*Catalog, error) {
	var s CatalogSigned
	env, err := k.verifyList(raw, KindCatalog, &s)
	if err != nil {
		return nil, err
	}
	issued, err := parseTime("issued", s.Issued)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%v", err)
	}
	expires, err := parseTime("expires", s.Expires)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%v", err)
	}
	if err := k.verifyEnvelope(env, KindCatalog, UseStore, StepList, issued); err != nil {
		return nil, err
	}
	if s.APIMajor != apiMajor {
		return nil, fail(CodeSignatureInvalid, StepList, "the catalog is for plugin API %d, not %d", s.APIMajor, apiMajor)
	}
	if !expires.After(issued) || expires.Sub(issued) > CatalogLifetime {
		return nil, fail(CodeSignatureInvalid, StepList, "the catalog claims %s to %s", s.Issued, s.Expires)
	}
	seen := st.CatalogVersion(apiMajor)
	if s.Version < seen {
		return nil, fail(CodeCatalogRollback, StepList, "version %d is older than %d, the newest seen", s.Version, seen)
	}
	if st.Now(clock).After(expires) {
		return nil, fail(CodeCatalogExpired, StepList, "version %d expired %s", s.Version, s.Expires)
	}
	return &Catalog{CatalogSigned: s, IssuedAt: issued, ExpiresAt: expires, SHA256: sha256Hex(raw), Raw: raw}, nil
}

// LoadStoredCatalog re-verifies a stored catalog's signature and form (its age is the
// caller's).
func (k *StoreKeys) LoadStoredCatalog(raw []byte, apiMajor int64) (*Catalog, error) {
	var s CatalogSigned
	env, err := k.verifyList(raw, KindCatalog, &s)
	if err != nil {
		return nil, err
	}
	issued, err := parseTime("issued", s.Issued)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%v", err)
	}
	expires, err := parseTime("expires", s.Expires)
	if err != nil {
		return nil, fail(CodeSignatureInvalid, StepList, "%v", err)
	}
	if err := k.verifyEnvelope(env, KindCatalog, UseStore, StepList, issued); err != nil {
		return nil, err
	}
	if s.APIMajor != apiMajor {
		return nil, fail(CodeSignatureInvalid, StepList, "the catalog is for plugin API %d, not %d", s.APIMajor, apiMajor)
	}
	return &Catalog{CatalogSigned: s, IssuedAt: issued, ExpiresAt: expires, SHA256: sha256Hex(raw), Raw: raw}, nil
}
