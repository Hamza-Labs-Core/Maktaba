package ca

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// Store keeps every certificate the CA issued (cluster_certs, slot 0150) and each
// machine's current one (cluster_nodes.cert_serial).
type Store interface {
	// Record adds an issued certificate; a machine's becomes its current one.
	Record(ctx context.Context, is *Issued) error
	// Current is a machine's current certificate serial ("" for none).
	Current(ctx context.Context, nodeID string) (string, error)
	// Revoked reports whether a serial was revoked (an unknown serial is not ours: true).
	Revoked(ctx context.Context, serial string) (bool, error)
	// RevokeNode revokes every certificate of a machine.
	RevokeNode(ctx context.Context, nodeID string, at time.Time) error
}

// SQLStore is the home database's store (Postgres placeholders).
type SQLStore struct{ DB *sql.DB }

// Record implements Store.
func (s SQLStore) Record(ctx context.Context, is *Issued) error {
	var node any
	if is.NodeID != "" {
		node = is.NodeID
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO cluster_certs (serial, node_id, kind, subject, sha256, not_before, not_after, issued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (serial) DO NOTHING`,
		is.Serial, node, is.Kind, is.Subject, is.SHA256, is.NotBefore, is.NotAfter, is.IssuedAt); err != nil {
		return err
	}
	if is.NodeID != "" && is.Kind == KindNode {
		_, err := s.DB.ExecContext(ctx, `UPDATE cluster_nodes SET cert_serial = $2, cert_not_after = $3 WHERE id = $1`,
			is.NodeID, is.Serial, is.NotAfter)
		return err
	}
	return nil
}

// Current implements Store.
func (s SQLStore) Current(ctx context.Context, nodeID string) (string, error) {
	var serial sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT cert_serial FROM cluster_nodes WHERE id = $1`, nodeID).Scan(&serial)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return serial.String, err
}

// Revoked implements Store.
func (s SQLStore) Revoked(ctx context.Context, serial string) (bool, error) {
	var revoked sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT revoked_at FROM cluster_certs WHERE serial = $1`, serial).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return revoked.Valid, nil
}

// RevokeNode implements Store.
func (s SQLStore) RevokeNode(ctx context.Context, nodeID string, at time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE cluster_certs SET revoked_at = $2 WHERE node_id = $1 AND revoked_at IS NULL`, nodeID, at)
	return err
}

// MemStore is a Store in memory (tests).
type MemStore struct {
	mu      sync.Mutex
	certs   map[string]Issued
	current map[string]string
}

func (m *MemStore) init() {
	if m.certs == nil {
		m.certs, m.current = map[string]Issued{}, map[string]string{}
	}
}

// Record implements Store.
func (m *MemStore) Record(_ context.Context, is *Issued) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.certs[is.Serial] = *is
	if is.NodeID != "" && is.Kind == KindNode {
		m.current[is.NodeID] = is.Serial
	}
	return nil
}

// Current implements Store.
func (m *MemStore) Current(_ context.Context, nodeID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	return m.current[nodeID], nil
}

// Revoked implements Store.
func (m *MemStore) Revoked(_ context.Context, serial string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	is, ok := m.certs[serial]
	return !ok || is.RevokedAt != nil, nil
}

// RevokeNode implements Store.
func (m *MemStore) RevokeNode(_ context.Context, nodeID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	for k, is := range m.certs {
		if is.NodeID == nodeID && is.RevokedAt == nil {
			t := at
			is.RevokedAt = &t
			m.certs[k] = is
		}
	}
	return nil
}

// Issued lists what was recorded (tests).
func (m *MemStore) Issued() []Issued {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Issued, 0, len(m.certs))
	for _, is := range m.certs {
		out = append(out, is)
	}
	return out
}
