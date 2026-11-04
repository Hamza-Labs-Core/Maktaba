package components

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// A component's settings are its catalog's rules.<section> entries, stored in app_settings
// (Epic 51, story 51.13). Disabling, stopping or removing a component never touches them;
// ResetSettings deletes them when the admin asks (and remove with purge does).
const (
	settingsKeyPrefix     = "rules."
	settingsVersionPrefix = "rules_version."
	// AuditSettingsReset is the audit action of one section's settings reset.
	AuditSettingsReset = "settings.reset"
)

// SectionSettings is one rules section a component owns and how many of its settings
// differ from the defaults.
type SectionSettings struct {
	Section string `json:"section"`
	Changed int    `json:"changed"`
}

// SettingsReset is what ResetSettings deleted.
type SettingsReset struct {
	ID       string            `json:"id"`
	Sections []SectionSettings `json:"sections"`
	// Changed is how many changed settings were deleted, over all the sections.
	Changed int `json:"changed"`
}

// sectionsOf is the rules sections component id owns, in the catalog's order.
func (s *Service) sectionsOf(id string) ([]string, error) {
	c, ok := s.cat().Get(id)
	if !ok {
		return nil, UnknownRefusal(id)
	}
	out := []string{}
	for _, name := range c.Settings {
		if section, ok := strings.CutPrefix(name, settingsKeyPrefix); ok {
			out = append(out, section)
		}
	}
	return out, nil
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func storedSetting(ctx context.Context, q rowQuerier, key string) (json.RawMessage, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func changedCount(raw json.RawMessage) int {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return 0
	}
	return len(m)
}

// SettingsOf is the sections component id owns, each with how many of its settings are
// changed (what a reset would lose).
func (s *Service) SettingsOf(ctx context.Context, id string) (SettingsReset, error) {
	sections, err := s.sectionsOf(id)
	if err != nil {
		return SettingsReset{}, err
	}
	out := SettingsReset{ID: id, Sections: []SectionSettings{}}
	for _, section := range sections {
		raw, err := storedSetting(ctx, s.Store.DB, settingsKeyPrefix+section)
		if err != nil {
			return SettingsReset{}, err
		}
		n := changedCount(raw)
		out.Sections = append(out.Sections, SectionSettings{Section: section, Changed: n})
		out.Changed += n
	}
	return out, nil
}

// ResetSettings deletes component id's rules.<section> and rules_version.<section> keys,
// so its settings are the defaults again, and writes one audit row per section that had
// either (its old value and version in before), all in one transaction. Each section is
// taken under the lock PATCH /api/rules/{section} takes. It does not check roles: its
// callers are admin-only.
func (s *Service) ResetSettings(ctx context.Context, id string, actor Actor) (SettingsReset, error) {
	sections, err := s.sectionsOf(id)
	if err != nil {
		return SettingsReset{}, err
	}
	db := s.Store.DB
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return SettingsReset{}, err
	}
	defer func() { _ = tx.Rollback() }()
	out := SettingsReset{ID: id, Sections: []SectionSettings{}}
	for _, section := range sections {
		key, vkey := settingsKeyPrefix+section, settingsVersionPrefix+section
		if !store.IsSQLite(db) {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
				return SettingsReset{}, err
			}
		}
		value, err := storedSetting(ctx, tx, key)
		if err != nil {
			return SettingsReset{}, err
		}
		version, err := storedSetting(ctx, tx, vkey)
		if err != nil {
			return SettingsReset{}, err
		}
		n := changedCount(value)
		out.Sections = append(out.Sections, SectionSettings{Section: section, Changed: n})
		out.Changed += n
		if value == nil && version == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_settings WHERE key = $1 OR key = $2`, key, vkey); err != nil {
			return SettingsReset{}, err
		}
		before := map[string]any{"section": section, "value": value, "version": version}
		payload, err := json.Marshal(map[string]any{"before": before, "after": nil, "by": actor.By, "section": section})
		if err != nil {
			return SettingsReset{}, err
		}
		var actorID any
		if _, perr := uuid.Parse(actor.UserID); perr == nil {
			actorID = actor.UserID
		}
		source := actor.Source
		if source == "" {
			source = "admin"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id,
			target_kind, actor_source, payload) VALUES ($1, $2, $3, $4, 'component', $5, $6)`,
			store.AuditCategory, AuditSettingsReset, actorID, id, source, string(payload)); err != nil {
			return SettingsReset{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return SettingsReset{}, err
	}
	return out, nil
}
