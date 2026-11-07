package symbols

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// Info is a kept table as the service admin lists it.
type Info struct {
	DebugID    string    `json:"debug_id"`
	CodeFile   string    `json:"code_file"`
	Arch       string    `json:"arch"`
	Version    string    `json:"version,omitempty"`
	UploadedAt time.Time `json:"uploaded_at"`
	Size       int       `json:"size"`
}

// Store is the symbol_tables table (slot 0053), with the last few tables read kept decoded
// (a report's crashes name the same few binaries).
type Store struct {
	DB *sql.DB

	mu    sync.Mutex
	cache []*Table
}

const cacheSize = 4

// Put keeps a table (encoded is what Encode made of it), replacing one with its debug id.
func (s *Store) Put(ctx context.Context, t *Table, encoded []byte, version string) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO symbol_tables (debug_id, code_file, arch, version, size, data) VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (debug_id) DO UPDATE SET code_file = EXCLUDED.code_file, arch = EXCLUDED.arch,
            version = EXCLUDED.version, size = EXCLUDED.size, data = EXCLUDED.data, uploaded_at = now()`,
		t.DebugID, t.CodeFile, t.Arch, clip(version, 64), len(encoded), encoded)
	if err == nil {
		s.mu.Lock()
		for i, c := range s.cache {
			if c.DebugID == t.DebugID {
				s.cache = append(s.cache[:i], s.cache[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}
	return err
}

// Find is the table of a binary (nil, nil when none is kept).
func (s *Store) Find(ctx context.Context, debugID string) (*Table, error) {
	debugID = NormalizeID(debugID)
	s.mu.Lock()
	for _, c := range s.cache {
		if c.DebugID == debugID {
			s.mu.Unlock()
			return c, nil
		}
	}
	s.mu.Unlock()
	var b []byte
	err := s.DB.QueryRowContext(ctx, `SELECT data FROM symbol_tables WHERE debug_id = $1`, debugID).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err := Decode(b)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache = append([]*Table{t}, s.cache...)
	if len(s.cache) > cacheSize {
		s.cache = s.cache[:cacheSize]
	}
	s.mu.Unlock()
	return t, nil
}

// List is the kept tables, the newest first (at most 500).
func (s *Store) List(ctx context.Context) ([]Info, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT debug_id, code_file, arch, version, uploaded_at, size
        FROM symbol_tables ORDER BY uploaded_at DESC, debug_id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Info{}
	for rows.Next() {
		var x Info
		if err := rows.Scan(&x.DebugID, &x.CodeFile, &x.Arch, &x.Version, &x.UploadedAt, &x.Size); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
