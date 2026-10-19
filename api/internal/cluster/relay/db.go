package relay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// Section is the rules section of the door's settings.
const Section = "relay"

// DBSettings reads rules.relay from app_settings.
func DBSettings(db *sql.DB) func(ctx context.Context) (Settings, error) {
	return func(ctx context.Context) (Settings, error) {
		m, err := rules.Effective(ctx, db, Section)
		if err != nil {
			return Settings{}, err
		}
		return SettingsFrom(m), nil
	}
}

// DBUpstream reads an extension's row (enabled, its address) and its
// secret from home_secrets (only the home's API holds the key).
func DBUpstream(db *sql.DB, store secrets.Store, getenv func(string) string) func(ctx context.Context, ext *Extension) (Upstream, error) {
	return func(ctx context.Context, ext *Extension) (Upstream, error) {
		var up Upstream
		var raw []byte
		err := db.QueryRowContext(ctx, `SELECT enabled, settings FROM extensions WHERE name = $1`, ext.Name).
			Scan(&up.Enabled, &raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Upstream{}, err
		}
		settings := map[string]any{}
		_ = json.Unmarshal(raw, &settings)
		if s, ok := settings[ext.URLKey].(string); ok {
			up.URL = strings.TrimSpace(s)
		}
		if up.URL == "" && getenv != nil && ext.URLEnv != "" {
			up.URL = strings.TrimSpace(getenv(ext.URLEnv))
		}
		if store == nil {
			return up, nil
		}
		v, err := store.Get(ctx, ext.SecretName)
		switch {
		case errors.Is(err, secrets.ErrNotFound):
		case err != nil:
			return up, err
		default:
			up.Secret = string(v)
		}
		return up, nil
	}
}
