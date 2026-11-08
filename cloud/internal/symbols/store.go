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
	// Kind is the table's kind as listed: ListDSYM, ListR8 or ListELF
	Kind string `json:"kind"`
	// CodeID is a native library's whole GNU build id, hex (its DebugID is the first 16 bytes)
	CodeID string `json:"code_id,omitempty"`
}

// The kinds the list names (a table's Kind is "" for a dSYM's).
const (
	ListDSYM = "dsym"
	ListR8   = KindR8
	ListELF  = KindELF
)

// listKind is a table's kind as the list names it.
func listKind(kind string) string {
	if kind == "" {
		return ListDSYM
	}
	return kind
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
        INSERT INTO symbol_tables (debug_id, code_file, arch, version, size, data, kind, code_id)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        ON CONFLICT (debug_id) DO UPDATE SET code_file = EXCLUDED.code_file, arch = EXCLUDED.arch,
            version = EXCLUDED.version, size = EXCLUDED.size, data = EXCLUDED.data, uploaded_at = now(),
            kind = EXCLUDED.kind, code_id = EXCLUDED.code_id`,
		t.DebugID, t.CodeFile, t.Arch, clip(version, 64), len(encoded), encoded, listKind(t.Kind), t.CodeID)
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

// List is the kept tables, the newest first (at most 500). A table kept before its kind was
// a column is read once to fill it in.
func (s *Store) List(ctx context.Context) ([]Info, error) {
	if err := s.fillKinds(ctx); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT debug_id, code_file, arch, version, uploaded_at, size,
            COALESCE(kind, ''), code_id
        FROM symbol_tables ORDER BY uploaded_at DESC, debug_id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Info{}
	for rows.Next() {
		var x Info
		if err := rows.Scan(&x.DebugID, &x.CodeFile, &x.Arch, &x.Version, &x.UploadedAt, &x.Size, &x.Kind,
			&x.CodeID); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// fillKinds sets the kind and build id of each table kept before they were columns (kind
// NULL), from the table itself, one at a time (a table is up to MaxTable). A table that no
// longer decodes keeps NULL and is listed with no kind.
func (s *Store) fillKinds(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT debug_id FROM symbol_tables WHERE kind IS NULL`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		var b []byte
		err := s.DB.QueryRowContext(ctx, `SELECT data FROM symbol_tables WHERE debug_id = $1 AND kind IS NULL`, id).Scan(&b)
		if errors.Is(err, sql.ErrNoRows) {
			continue // replaced or removed meanwhile
		}
		if err != nil {
			return err
		}
		t, err := Decode(b)
		if err != nil {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE symbol_tables SET kind = $2, code_id = $3
            WHERE debug_id = $1 AND kind IS NULL`, id, listKind(t.Kind), t.CodeID); err != nil {
			return err
		}
	}
	return nil
}
