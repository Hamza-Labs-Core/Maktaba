package cloudlink

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// Identity is this home's identity in Maktaba Cloud, read from the file
// the relay agent writes beside its sealed credentials
// (cloudlink.identity.json: cloud/internal/cloudlink IdentityFile).
type Identity struct {
	ServerID     string `json:"server_id"`
	Slug         string `json:"slug"`
	AssertionKey string `json:"assertion_key"`
	CloudAPI     string `json:"cloud_api"`
}

// ErrNoIdentity: this home is not claimed in the cloud (or the agent has
// not written its identity yet), so it has no linked accounts.
var ErrNoIdentity = errors.New("cloudlink: this home is not linked to Maktaba Cloud")

// IdentitySource reads the identity file, again whenever it changes (the
// agent writes it after the API started, at the first claim).
type IdentitySource struct {
	Path string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	id      Identity
	pub     ed25519.PublicKey
	err     error
}

// Load returns the identity and the pinned key, or ErrNoIdentity.
func (s *IdentitySource) Load() (Identity, ed25519.PublicKey, error) {
	if s == nil || s.Path == "" {
		return Identity{}, nil, ErrNoIdentity
	}
	st, err := os.Stat(s.Path)
	if err != nil {
		return Identity{}, nil, ErrNoIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.ModTime().Equal(s.modTime) && st.Size() == s.size && (s.pub != nil || s.err != nil) {
		return s.id, s.pub, s.err
	}
	s.modTime, s.size = st.ModTime(), st.Size()
	s.id, s.pub, s.err = Identity{}, nil, nil
	b, err := os.ReadFile(s.Path)
	if err != nil {
		s.err = ErrNoIdentity
		return Identity{}, nil, s.err
	}
	var id Identity
	if err := json.Unmarshal(b, &id); err != nil || id.ServerID == "" {
		s.err = ErrNoIdentity
		return Identity{}, nil, s.err
	}
	pub, err := ParsePublicKey(id.AssertionKey)
	if err != nil {
		s.err = ErrNoIdentity
		return Identity{}, nil, s.err
	}
	s.id, s.pub = id, pub
	return id, pub, nil
}
