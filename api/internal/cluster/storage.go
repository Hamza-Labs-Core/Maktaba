package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

// StorageServer is a storage_servers row (story 31.3): the identity a
// canonical path smb://<name>/<share>/... names, and its addresses.
type StorageServer struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Addresses []string          `json:"addresses"`
	CharMap   string            `json:"char_map"`
	Exports   map[string]string `json:"exports"`
}

// ErrBadServer is returned for an invalid server name.
var ErrBadServer = errors.New("cluster: a server name is lower case, without '/'")

// NormaliseServer lower-cases the name, trims and dedups the addresses.
func NormaliseServer(s StorageServer) (StorageServer, error) {
	s.Name = strings.ToLower(strings.TrimSpace(s.Name))
	if s.Name == "" || strings.Contains(s.Name, "/") || len(s.Name) > 253 {
		return s, ErrBadServer
	}
	seen := map[string]bool{}
	addrs := []string{}
	for _, a := range s.Addresses {
		a = strings.TrimSpace(a)
		if a == "" || seen[strings.ToLower(a)] {
			continue
		}
		seen[strings.ToLower(a)] = true
		addrs = append(addrs, a)
	}
	s.Addresses = addrs
	if strings.TrimSpace(s.CharMap) == "" {
		s.CharMap = "none"
	}
	if s.Exports == nil {
		s.Exports = map[string]string{}
	}
	return s, nil
}

// StorageStore holds the shares' side of the cluster: the storage servers,
// the home's per-share storage report and its "share -> local mount" override.
type StorageStore interface {
	// HomeStorage is instance_identity.storage and .path_map (nil: none).
	HomeStorage(ctx context.Context) (storage, pathMap json.RawMessage, err error)
	// SetHomePathMap sets the home's override; nil clears it.
	SetHomePathMap(ctx context.Context, pathMap json.RawMessage) error
	Servers(ctx context.Context) ([]StorageServer, error)
	PutServer(ctx context.Context, s StorageServer) (StorageServer, error)
	DeleteServer(ctx context.Context, name string) error
}

// PGStorage is the Postgres StorageStore.
type PGStorage struct{ DB *sql.DB }

var _ StorageStore = (*PGStorage)(nil)

func (p *PGStorage) HomeStorage(ctx context.Context) (json.RawMessage, json.RawMessage, error) {
	var st, pm sql.NullString
	err := p.DB.QueryRowContext(ctx,
		`SELECT storage::text, path_map::text FROM instance_identity WHERE id = 1`).Scan(&st, &pm)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return rawOrNilStr(st), rawOrNilStr(pm), nil
}

func rawOrNilStr(s sql.NullString) json.RawMessage {
	if !s.Valid || s.String == "" || s.String == "null" {
		return nil
	}
	return json.RawMessage(s.String)
}

func (p *PGStorage) SetHomePathMap(ctx context.Context, pm json.RawMessage) error {
	var arg any
	if pm != nil {
		arg = string(pm)
	}
	_, err := p.DB.ExecContext(ctx,
		`UPDATE instance_identity SET path_map = $1::jsonb, updated_at = now() WHERE id = 1`, arg)
	return err
}

func (p *PGStorage) Servers(ctx context.Context) ([]StorageServer, error) {
	rows, err := p.DB.QueryContext(ctx,
		`SELECT id::text, name, addresses::text, char_map, exports::text FROM storage_servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StorageServer{}
	for rows.Next() {
		var s StorageServer
		var addrs, exports string
		if err := rows.Scan(&s.ID, &s.Name, &addrs, &s.CharMap, &exports); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(addrs), &s.Addresses)
		_ = json.Unmarshal([]byte(exports), &s.Exports)
		if s.Addresses == nil {
			s.Addresses = []string{}
		}
		if s.Exports == nil {
			s.Exports = map[string]string{}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *PGStorage) PutServer(ctx context.Context, s StorageServer) (StorageServer, error) {
	s, err := NormaliseServer(s)
	if err != nil {
		return s, err
	}
	addrs, _ := json.Marshal(s.Addresses)
	exports, _ := json.Marshal(s.Exports)
	err = p.DB.QueryRowContext(ctx, `
		INSERT INTO storage_servers (name, addresses, char_map, exports)
		VALUES ($1, $2::jsonb, $3, $4::jsonb)
		ON CONFLICT (name) DO UPDATE SET addresses = EXCLUDED.addresses,
			char_map = EXCLUDED.char_map, exports = EXCLUDED.exports, updated_at = now()
		RETURNING id::text`, s.Name, string(addrs), s.CharMap, string(exports)).Scan(&s.ID)
	return s, err
}

func (p *PGStorage) DeleteServer(ctx context.Context, name string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM storage_servers WHERE name = $1`, strings.ToLower(name))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MemoryStorage is the in-memory StorageStore (SQLite homes, tests).
type MemoryStorage struct {
	mu      sync.Mutex
	Storage json.RawMessage
	PathMap json.RawMessage
	servers map[string]StorageServer
}

var _ StorageStore = (*MemoryStorage)(nil)

func (m *MemoryStorage) HomeStorage(context.Context) (json.RawMessage, json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Storage, m.PathMap, nil
}

func (m *MemoryStorage) SetHomePathMap(_ context.Context, pm json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PathMap = pm
	return nil
}

func (m *MemoryStorage) Servers(context.Context) ([]StorageServer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []StorageServer{}
	for _, s := range m.servers {
		out = append(out, s)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func (m *MemoryStorage) PutServer(_ context.Context, s StorageServer) (StorageServer, error) {
	s, err := NormaliseServer(s)
	if err != nil {
		return s, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.servers == nil {
		m.servers = map[string]StorageServer{}
	}
	if old, ok := m.servers[s.Name]; ok {
		s.ID = old.ID
	} else {
		s.ID = "mem-" + s.Name
	}
	m.servers[s.Name] = s
	return s, nil
}

func (m *MemoryStorage) DeleteServer(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	name = strings.ToLower(name)
	if _, ok := m.servers[name]; !ok {
		return ErrNotFound
	}
	delete(m.servers, name)
	return nil
}
