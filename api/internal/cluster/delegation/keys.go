package delegation

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// Key lifetimes of design §10.2: rotated daily, the previous key verifies for 10
// minutes after a rotation (requests in flight, EC1).
const (
	RotateEvery = 24 * time.Hour
	Overlap     = 10 * time.Minute
)

// SecretName is where a node's keys are sealed in home_secrets.
func SecretName(nodeID string) string { return "cluster.node." + nodeID + ".delegation" }

type storedKey struct {
	ID        string    `json:"id"`
	Secret    []byte    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
	RetiredAt time.Time `json:"retired_at,omitempty"`
}

type nodeKeys struct {
	Current  *storedKey `json:"current"`
	Previous *storedKey `json:"previous,omitempty"`
}

// KeyStore issues, rotates, looks up and revokes the nodes' delegation keys on the
// home. Keys are sealed in home_secrets when the home has a secrets key; without one
// (or on a store error) they live in memory only, and a node simply receives a new key
// on its next channel session.
type KeyStore struct {
	Secrets secrets.Store // nil: memory only
	DB      *sql.DB       // cluster_nodes.delegation_key_id / delegation_rotated_at; nil: none
	Now     func() time.Time
	Logger  *slog.Logger

	mu    sync.Mutex
	cache map[string]*nodeKeys
}

func (s *KeyStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func newSecret() ([]byte, string, error) {
	b := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, "", err
	}
	id := make([]byte, 6)
	if _, err := io.ReadFull(rand.Reader, id); err != nil {
		return nil, "", err
	}
	return b, "dk_" + hex.EncodeToString(id), nil
}

// load returns the node's keys (cache, then home_secrets); s.mu is held.
func (s *KeyStore) load(ctx context.Context, nodeID string) *nodeKeys {
	if s.cache == nil {
		s.cache = map[string]*nodeKeys{}
	}
	if k, ok := s.cache[nodeID]; ok {
		return k
	}
	if s.Secrets != nil {
		if raw, err := s.Secrets.Get(ctx, SecretName(nodeID)); err == nil && len(raw) > 0 {
			var k nodeKeys
			if json.Unmarshal(raw, &k) == nil && k.Current != nil {
				s.cache[nodeID] = &k
				return &k
			}
		}
	}
	return nil
}

func (s *KeyStore) save(ctx context.Context, nodeID string, k *nodeKeys) {
	s.cache[nodeID] = k
	if s.Secrets != nil {
		raw, _ := json.Marshal(k)
		if err := s.Secrets.Put(ctx, SecretName(nodeID), raw, ""); err != nil && s.Logger != nil {
			s.Logger.Warn("cluster: delegation key kept in memory only (home_secrets refused it)",
				"node_id", nodeID, "err", err, "event", "delegation_key_unsealed")
		}
	}
	if s.DB != nil && k.Current != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE cluster_nodes SET delegation_key_id = $2, delegation_rotated_at = $3 WHERE id = $1`,
			nodeID, k.Current.ID, k.Current.CreatedAt)
	}
}

// Issue returns the node's current key, creating it, or rotating it when older than a
// day. rotated reports a new key (the hub then sends it on the node's channel).
func (s *KeyStore) Issue(ctx context.Context, nodeID string) (Key, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	k := s.load(ctx, nodeID)
	if k != nil && k.Current != nil && now.Sub(k.Current.CreatedAt) < RotateEvery {
		return toKey(k.Current), false, nil
	}
	secret, id, err := newSecret()
	if err != nil {
		return Key{}, false, err
	}
	next := &nodeKeys{Current: &storedKey{ID: id, Secret: secret, CreatedAt: now}}
	if k != nil && k.Current != nil {
		prev := *k.Current
		prev.RetiredAt = now
		next.Previous = &prev
	}
	s.save(ctx, nodeID, next)
	if s.Logger != nil {
		s.Logger.Info("cluster: delegation key issued", "node_id", nodeID, "key_id", id,
			"rotation", k != nil, "event", "delegation_key_issued")
	}
	return toKey(next.Current), true, nil
}

func toKey(k *storedKey) Key {
	return Key{ID: k.ID, Secret: append([]byte(nil), k.Secret...), NotAfter: k.CreatedAt.Add(RotateEvery + Overlap)}
}

// Lookup implements KeyLookup: the current key, or the previous one for Overlap after
// its rotation.
func (s *KeyStore) Lookup(ctx context.Context, nodeID, keyID string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.load(ctx, nodeID)
	if k == nil {
		return nil, false
	}
	now := s.now()
	if k.Current != nil && k.Current.ID == keyID && now.Sub(k.Current.CreatedAt) < RotateEvery+Overlap {
		return append([]byte(nil), k.Current.Secret...), true
	}
	if k.Previous != nil && k.Previous.ID == keyID && now.Sub(k.Previous.RetiredAt) <= Overlap {
		return append([]byte(nil), k.Previous.Secret...), true
	}
	return nil, false
}

// Revoke deletes both keys at once (the machine was revoked or removed, EC4).
func (s *KeyStore) Revoke(ctx context.Context, nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache != nil {
		delete(s.cache, nodeID)
	}
	if s.Secrets != nil {
		_ = s.Secrets.Delete(ctx, SecretName(nodeID))
	}
	if s.DB != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE cluster_nodes SET delegation_key_id = NULL WHERE id = $1`, nodeID)
	}
}
