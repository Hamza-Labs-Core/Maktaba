package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is an in-process Store: handler tests use it, and so does a
// home that cannot take machines (SQLite), which only needs an identity
// for the mDNS advertisement and an empty registry.
type MemoryStore struct {
	mu       sync.Mutex
	identity *Identity
	nodes    map[string]*Node
	stages   map[string]map[string]bool
	workers  []NodeWorker
	// KnownStages, when non-nil, is the job_stages registry SetStages
	// checks against.
	KnownStages map[string]bool
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nodes: map[string]*Node{}, stages: map[string]map[string]bool{}}
}

// AddWorker registers a node worker (tests).
func (m *MemoryStore) AddWorker(w NodeWorker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workers = append(m.workers, w)
}

// EnsureIdentity implements Store.
func (m *MemoryStore) EnsureIdentity(_ context.Context, name string) (Identity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.identity == nil {
		now := time.Now().UTC()
		m.identity = &Identity{InstanceID: uuid.NewString(), Name: name, CreatedAt: now, UpdatedAt: now}
	}
	return *m.identity, nil
}

// Identity implements Store.
func (m *MemoryStore) Identity(ctx context.Context) (Identity, error) {
	m.mu.Lock()
	ok := m.identity != nil
	m.mu.Unlock()
	if !ok {
		return m.EnsureIdentity(ctx, "maktaba")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.identity, nil
}

// SetAcceptJoins implements Store.
func (m *MemoryStore) SetAcceptJoins(ctx context.Context, on bool, until *time.Time) (Identity, error) {
	if _, err := m.Identity(ctx); err != nil {
		return Identity{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identity.AcceptJoins = on
	m.identity.AcceptJoinsUntil = until
	m.identity.UpdatedAt = time.Now().UTC()
	return *m.identity, nil
}

// CloseExpiredJoins implements Store.
func (m *MemoryStore) CloseExpiredJoins(_ context.Context, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.identity
	if i == nil || !i.AcceptJoins || i.AcceptJoinsUntil == nil || i.AcceptJoinsUntil.After(now) {
		return false, nil
	}
	i.AcceptJoins, i.AcceptJoinsUntil = false, nil
	return true, nil
}

// SetName implements Store.
func (m *MemoryStore) SetName(ctx context.Context, name string) (Identity, error) {
	if _, err := m.Identity(ctx); err != nil {
		return Identity{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identity.Name = name
	return *m.identity, nil
}

// Node implements Store.
func (m *MemoryStore) Node(_ context.Context, id string) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[id]
	if !ok {
		return Node{}, ErrNotFound
	}
	return *n, nil
}

// Nodes implements Store.
func (m *MemoryStore) Nodes(_ context.Context) ([]Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// PutPending implements Store.
func (m *MemoryStore) PutPending(_ context.Context, n Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.nodes[n.ID]; ok && old.State != StatePending && old.State != StateRejected {
		return ErrAlreadyMember
	}
	n.State = StatePending
	n.CodeAttempts = 0
	n.Sealed = nil
	n.UpdatedAt = n.CreatedAt
	if n.Versions == nil {
		n.Versions = json.RawMessage("{}")
	}
	if n.Capabilities == nil {
		n.Capabilities = json.RawMessage("{}")
	}
	if n.Storage == nil {
		n.Storage = json.RawMessage("{}")
	}
	m.nodes[n.ID] = &n
	return nil
}

func (m *MemoryStore) get(id, state string) (*Node, error) {
	n, ok := m.nodes[id]
	if !ok || (state != "" && n.State != state) {
		return nil, ErrNotFound
	}
	return n, nil
}

// WrongCode implements Store.
func (m *MemoryStore) WrongCode(_ context.Context, id string, maxAttempts int, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, err := m.get(id, StatePending)
	if err != nil {
		return 0, err
	}
	n.CodeAttempts++
	if n.CodeAttempts >= maxAttempts {
		n.State = StateRejected
		n.HomePrivkey = nil
	}
	n.UpdatedAt = now
	return n.CodeAttempts, nil
}

// Approve implements Store.
func (m *MemoryStore) Approve(_ context.Context, id, dbRole string, sealed json.RawMessage, by string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, err := m.get(id, StatePending)
	if err != nil {
		return err
	}
	n.State, n.DBRole, n.Sealed, n.ApprovedBy = StateApproved, &dbRole, sealed, &by
	n.ApprovedAt, n.HomePrivkey, n.RevokedAt, n.UpdatedAt = &now, nil, nil, now
	return nil
}

// Reject implements Store.
func (m *MemoryStore) Reject(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, err := m.get(id, StatePending)
	if err != nil {
		return err
	}
	n.State, n.HomePrivkey, n.Sealed, n.UpdatedAt = StateRejected, nil, nil, now
	return nil
}

// Revoke implements Store.
func (m *MemoryStore) Revoke(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, err := m.get(id, StateApproved)
	if err != nil {
		return err
	}
	n.State, n.HomePrivkey, n.Sealed, n.RevokedAt, n.UpdatedAt = StateRevoked, nil, nil, &now, now
	return nil
}

// UpdateNode implements Store.
func (m *MemoryStore) UpdateNode(_ context.Context, id string, p NodePatch, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, err := m.get(id, "")
	if err != nil {
		return err
	}
	if p.Name != nil {
		n.Name = *p.Name
	}
	if p.Drain != nil {
		n.Drain = *p.Drain
	}
	switch {
	case p.PathMap != nil:
		src := PathMapAdmin
		n.PathMap, n.PathMapSource = p.PathMap, &src
	case p.ClearPathMap:
		n.PathMap, n.PathMapSource = nil, nil
	}
	n.UpdatedAt = now
	return nil
}

// DeleteNode implements Store.
func (m *MemoryStore) DeleteNode(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nodes[id]; !ok {
		return ErrNotFound
	}
	delete(m.nodes, id)
	delete(m.stages, id)
	return nil
}

// SetStages implements Store.
func (m *MemoryStore) SetStages(_ context.Context, id string, stages map[string]*bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for s := range stages {
		if m.KnownStages != nil && !m.KnownStages[s] {
			return fmt.Errorf("%w: %s", ErrUnknownStage, s)
		}
	}
	if m.stages[id] == nil {
		m.stages[id] = map[string]bool{}
	}
	for s, on := range stages {
		if on == nil {
			delete(m.stages[id], s)
		} else {
			m.stages[id][s] = *on
		}
	}
	return nil
}

// Stages implements Store.
func (m *MemoryStore) Stages(_ context.Context) (map[string]map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]map[string]bool{}
	for id, ss := range m.stages {
		out[id] = map[string]bool{}
		for s, on := range ss {
			out[id][s] = on
		}
	}
	return out, nil
}

// Workers implements Store.
func (m *MemoryStore) Workers(_ context.Context) ([]NodeWorker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]NodeWorker{}, m.workers...), nil
}

// ClearSealed implements Store.
func (m *MemoryStore) ClearSealed(_ context.Context, cutoff time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	registered := map[string]bool{}
	for _, w := range m.workers {
		registered[w.NodeID] = true
	}
	for _, n := range m.nodes {
		if n.Sealed == nil || n.State != StateApproved {
			continue
		}
		if registered[n.ID] || (n.ApprovedAt != nil && n.ApprovedAt.Before(cutoff)) {
			n.Sealed = nil
		}
	}
	return nil
}
