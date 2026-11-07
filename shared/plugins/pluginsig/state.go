package pluginsig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// State is what a home remembers to refuse rollbacks (§3, §4): the newest version of each
// list it has accepted (and the sha256 of that version, so the same number with other
// content is refused), and the newest issue time it has seen. A home keeps it in
// <data>/state/plugins-trust.json and in Postgres, and uses the newer of the two (Merge),
// so restoring an old copy of either cannot lower it.
type State struct {
	StoreKeysVersion int64            `json:"store_keys_version"`
	StoreKeysSHA256  string           `json:"store_keys_sha256,omitempty"`
	RevokedVersion   int64            `json:"revoked_version"`
	RevokedSHA256    string           `json:"revoked_sha256,omitempty"`
	CatalogVersions  map[string]int64 `json:"catalog_versions,omitempty"`
	NewestIssued     time.Time        `json:"newest_issued"`
}

// Now is the home's time for expiry: the newest of the clock and the newest issue time
// seen, so a clock set in the past cannot make an old list look fresh (§ edge cases).
func (s State) Now(clock time.Time) time.Time {
	if s.NewestIssued.After(clock) {
		return s.NewestIssued
	}
	return clock
}

// CatalogVersion is the newest catalog version seen for an API major.
func (s State) CatalogVersion(apiMajor int64) int64 {
	return s.CatalogVersions[strconv.FormatInt(apiMajor, 10)]
}

func (s *State) issued(t time.Time) {
	if t.After(s.NewestIssued) {
		s.NewestIssued = t.UTC()
	}
}

// AcceptStoreKeys records a verified store-keys.json.
func (s *State) AcceptStoreKeys(k *StoreKeys) {
	if k.Version >= s.StoreKeysVersion {
		s.StoreKeysVersion, s.StoreKeysSHA256 = k.Version, k.SHA256
	}
}

// AcceptRevoked records a verified revocation list.
func (s *State) AcceptRevoked(r *Revoked) {
	if r.Version >= s.RevokedVersion {
		s.RevokedVersion, s.RevokedSHA256 = r.Version, r.SHA256
	}
	s.issued(r.IssuedAt)
}

// AcceptRevokedHead records a verified head's issue time (its version is the list's to set).
func (s *State) AcceptRevokedHead(h *RevokedHead) { s.issued(h.IssuedAt) }

// AcceptCatalog records a verified catalog.
func (s *State) AcceptCatalog(c *Catalog) {
	if s.CatalogVersions == nil {
		s.CatalogVersions = map[string]int64{}
	}
	key := strconv.FormatInt(c.APIMajor, 10)
	if c.Version > s.CatalogVersions[key] {
		s.CatalogVersions[key] = c.Version
	}
	s.issued(c.IssuedAt)
}

// Merge takes the newer of each field of o (a second copy of the state).
func (s *State) Merge(o State) {
	if o.StoreKeysVersion > s.StoreKeysVersion {
		s.StoreKeysVersion, s.StoreKeysSHA256 = o.StoreKeysVersion, o.StoreKeysSHA256
	} else if o.StoreKeysVersion == s.StoreKeysVersion && s.StoreKeysSHA256 == "" {
		s.StoreKeysSHA256 = o.StoreKeysSHA256
	}
	if o.RevokedVersion > s.RevokedVersion {
		s.RevokedVersion, s.RevokedSHA256 = o.RevokedVersion, o.RevokedSHA256
	} else if o.RevokedVersion == s.RevokedVersion && s.RevokedSHA256 == "" {
		s.RevokedSHA256 = o.RevokedSHA256
	}
	for k, v := range o.CatalogVersions {
		if s.CatalogVersions == nil {
			s.CatalogVersions = map[string]int64{}
		}
		if v > s.CatalogVersions[k] {
			s.CatalogVersions[k] = v
		}
	}
	s.issued(o.NewestIssued)
}

// TrustFile is the file copy of the state, <data>/state/plugins-trust.json.
func TrustFile(stateDir string) string { return filepath.Join(stateDir, "plugins-trust.json") }

// LoadState reads a state file; a missing file is the zero state.
func LoadState(path string) (State, error) {
	var s State
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

// SaveState writes a state file atomically (a temporary file renamed over it). It never
// lowers what the file holds: the file's state is merged in first.
func SaveState(path string, s State) error {
	if old, err := LoadState(path); err == nil {
		s.Merge(old)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plugins-trust-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
