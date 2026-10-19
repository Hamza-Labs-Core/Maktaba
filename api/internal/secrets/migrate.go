package secrets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ExtensionSecretFields lists, per extension, its secret setting keys
// (the extension catalogue's `secret` fields) and the home_secrets name
// each is kept under: extension -> setting key -> secret name.
type ExtensionSecretFields map[string]map[string]string

// MigrateReport says what MigrateExtensionSecrets did.
type MigrateReport struct {
	// Moved are the home_secrets names written from extensions.settings.
	Moved []string
	// Dropped are settings removed because home_secrets already held a
	// value (a newer one, written by the API).
	Dropped []string
}

// MigrateExtensionSecrets moves every extension secret still stored in
// extensions.settings (the Plex token before slot 0120) into home_secrets
// and removes it from the settings, extension by extension, each in one
// transaction (story 32.6 AC6a, README Q7). It is idempotent: run at every
// start of the home's API, it finds nothing to do after the first.
//
// A value already in home_secrets wins (only the API writes there, after
// this story), so a stale copy in the settings is dropped, not moved.
func MigrateExtensionSecrets(ctx context.Context, s *DBStore, fields ExtensionSecretFields) (MigrateReport, error) {
	var rep MigrateReport
	if s == nil || s.DB == nil {
		return rep, errors.New("secrets: no store")
	}
	for ext, keys := range fields {
		if len(keys) == 0 {
			continue
		}
		moved, dropped, err := s.migrateOne(ctx, ext, keys)
		if err != nil {
			return rep, fmt.Errorf("secrets: move %s secrets: %w", ext, err)
		}
		rep.Moved = append(rep.Moved, moved...)
		rep.Dropped = append(rep.Dropped, dropped...)
	}
	return rep, nil
}

func (s *DBStore) migrateOne(ctx context.Context, ext string, keys map[string]string) (moved, dropped []string, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT settings FROM extensions WHERE name = $1`, ext).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	settings := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, nil, fmt.Errorf("settings are not JSON: %w", err)
		}
	}
	changed := false
	for k, name := range keys {
		v, ok := settings[k]
		if !ok {
			continue
		}
		delete(settings, k)
		changed = true
		str, _ := v.(string)
		str = strings.TrimSpace(str)
		if str == "" {
			continue
		}
		var one int
		herr := tx.QueryRowContext(ctx, `SELECT 1 FROM home_secrets WHERE name = $1`, name).Scan(&one)
		switch {
		case herr == nil:
			dropped = append(dropped, name)
			continue
		case !errors.Is(herr, sql.ErrNoRows):
			return nil, nil, herr
		}
		if err := s.put(ctx, tx, name, []byte(str), ""); err != nil {
			return nil, nil, err
		}
		moved = append(moved, name)
	}
	if !changed {
		return nil, nil, nil
	}
	blob, _ := json.Marshal(settings)
	if _, err := tx.ExecContext(ctx,
		`UPDATE extensions SET settings = $2, updated_at = $3 WHERE name = $1`, ext, string(blob), s.now()); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return moved, dropped, nil
}
