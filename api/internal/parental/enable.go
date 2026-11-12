package parental

// Turning parental controls on in a home that already exists (Epic 57 story 57.12).
//
// The switch is app_settings `parental.enabled`. It starts off (slot 0225), and while
// it is off a device's signal is recorded and limits nothing: no `ac` claim is minted
// and no request is held to it, so an update changes what no one sees. It turns on by
// itself when an admin makes the first account restricted or gives the first library
// a default tier, and an admin can turn it on or off; off is refused while any
// account is restricted.
//
// A restricted account's policy does not read the switch: a policy that exists is
// always applied. Only the device signals wait for it. A value that is missing or
// cannot be read counts as on, so a fault never loosens a limit.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SettingEnabled is the app_settings key of the switch; SettingNoticeDismissed the
// time an admin last dismissed the notice that a child's device signed in.
const (
	SettingEnabled         = "parental.enabled"
	SettingNoticeDismissed = "parental.notice_dismissed_at"
)

// The audit actions of the switch.
const (
	AuditEnabled  = "parental.enabled"
	AuditDisabled = "parental.disabled"
)

// ErrRestrictionsExist: the switch cannot go off while an account is restricted.
var ErrRestrictionsExist = errors.New("parental: restricted accounts exist")

// enabledValue reads the stored JSON: only `false` is off.
func enabledValue(raw sql.NullString) bool {
	return !raw.Valid || strings.TrimSpace(raw.String) != "false"
}

// Enabled reports whether the switch is on.
func (s *Store) Enabled(ctx context.Context) (bool, error) {
	return enabledIn(ctx, s.DB)
}

func enabledIn(ctx context.Context, q queryer) (bool, error) {
	var raw sql.NullString
	err := q.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = $1`, SettingEnabled).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, fmt.Errorf("parental: enabled: %w", err)
	}
	return enabledValue(raw), nil
}

// auditSetting writes the audit row of a change of the switch. The actor is kept when
// it is a user of this home (the admin token has no user row).
func auditSetting(ctx context.Context, tx *sql.Tx, action, actorID string, before, after bool) error {
	payload, err := json.Marshal(map[string]any{"before": before, "after": after})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO audit_log (category, action, actor_user_id, target_id, target_kind, payload)
		 VALUES ('admin', $1, (SELECT id FROM users WHERE id = $2::uuid), $3, 'setting', $4)`,
		action, nullUUID(actorID), SettingEnabled, string(payload))
	return err
}

// writeEnabled stores the switch and, when it changed, writes the audit row and tells
// every process to read its limits again when tx commits.
func writeEnabled(ctx context.Context, tx *sql.Tx, actorID string, on bool) error {
	was, err := enabledIn(ctx, tx)
	if err != nil {
		return err
	}
	value := "false"
	if on {
		value = "true"
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO app_settings (key, value, updated_by) VALUES ($1, $2::jsonb, (SELECT id FROM users WHERE id = $3::uuid))
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`,
		SettingEnabled, value, nullUUID(actorID)); err != nil {
		return err
	}
	if was == on {
		return nil
	}
	action := AuditDisabled
	if on {
		action = AuditEnabled
	}
	if err := auditSetting(ctx, tx, action, actorID, was, on); err != nil {
		return err
	}
	return notifySignal(ctx, tx, "")
}

// EnableTx turns the switch on inside tx (the first restricted account, the first
// library default tier). It does nothing when it is already on.
func EnableTx(ctx context.Context, tx *sql.Tx, actorID string) error {
	on, err := enabledIn(ctx, tx)
	if err != nil || on {
		return err
	}
	return writeEnabled(ctx, tx, actorID, true)
}

// SetEnabled turns the switch on or off for an admin. ErrRestrictionsExist when it is
// asked off while an account is restricted.
func (s *Store) SetEnabled(ctx context.Context, actorID string, on bool) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if !on {
			// the policies are locked against a new one while this decides
			if _, err := tx.ExecContext(ctx, `LOCK TABLE parental_policies IN SHARE ROW EXCLUSIVE MODE`); err != nil {
				return err
			}
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM parental_policies`).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return ErrRestrictionsExist
			}
		}
		return writeEnabled(ctx, tx, actorID, on)
	})
}

// Settings is the switch as an admin reads it.
type Settings struct {
	Enabled bool `json:"enabled"`
	// RestrictedAccounts is how many accounts are restricted; CanDisable is false
	// while there is one.
	RestrictedAccounts int  `json:"restricted_accounts"`
	CanDisable         bool `json:"can_disable"`
	// ChildDevices is how many sign-ins' devices said a child may be using them (a
	// limit under 18) and are not applied because the switch is off.
	ChildDevices int `json:"child_devices"`
	// ChildDeviceNotice says to show the notice: the switch is off and such a device
	// spoke since an admin last dismissed the notice.
	ChildDeviceNotice bool `json:"child_device_notice"`
}

// Settings reads the switch and what an admin is told with it.
func (s *Store) Settings(ctx context.Context) (*Settings, error) {
	var (
		raw       sql.NullString
		out       Settings
		unnoticed int
	)
	// the dismissal is stored as epoch seconds; a signal seen after it shows the notice again
	err := s.DB.QueryRowContext(ctx,
		`SELECT (SELECT value::text FROM app_settings WHERE key = $1),
		        (SELECT count(*) FROM parental_policies),
		        (SELECT count(*) FROM session_age_signals WHERE tier_limit IS NOT NULL AND tier_limit < $3),
		        (SELECT count(*) FROM session_age_signals g
		          WHERE g.tier_limit IS NOT NULL AND g.tier_limit < $3
		            AND g.observed_at > to_timestamp(COALESCE(
		                  (SELECT (value #>> '{}')::double precision FROM app_settings WHERE key = $2), 0)))`,
		SettingEnabled, SettingNoticeDismissed, Tier18).Scan(&raw, &out.RestrictedAccounts, &out.ChildDevices, &unnoticed)
	if err != nil {
		return nil, fmt.Errorf("parental: settings: %w", err)
	}
	out.Enabled = enabledValue(raw)
	out.CanDisable = out.RestrictedAccounts == 0
	if out.Enabled {
		out.ChildDevices = 0
	}
	out.ChildDeviceNotice = !out.Enabled && unnoticed > 0
	return &out, nil
}

// DismissNotice records that an admin dismissed the child-device notice: it stays
// away until another device speaks.
func (s *Store) DismissNotice(ctx context.Context, actorID string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO app_settings (key, value, updated_by)
		 VALUES ($1, to_jsonb(extract(epoch FROM clock_timestamp())), (SELECT id FROM users WHERE id = $2::uuid))
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`,
		SettingNoticeDismissed, nullUUID(actorID))
	if err != nil {
		return fmt.Errorf("parental: dismiss notice: %w", err)
	}
	return nil
}

// The content types (videos.content_type, Epic 26) that count as lectures when a
// library's default tier is suggested, and the share of a library's classified
// videos they must reach.
var lectureTypes = map[string]bool{"lecture": true, "sermon": true}

const lectureShare = 0.9

// SuggestTier is the default tier Rate libraries offers for a library whose videos
// have these content types (type to count; "" and "unknown" are not classified): All
// ages when lectures are at least nine in ten of the classified ones, else none. It
// only fills the form in: nothing is stored until an admin confirms.
func SuggestTier(byType map[string]int) *int {
	lectures, classified := 0, 0
	for typ, n := range byType {
		if typ == "" || typ == "unknown" || n <= 0 {
			continue
		}
		classified += n
		if lectureTypes[typ] {
			lectures += n
		}
	}
	if classified == 0 || float64(lectures) < lectureShare*float64(classified) {
		return nil
	}
	tier := TierAll
	return &tier
}

// LibrarySuggestion is one library in Rate libraries.
type LibrarySuggestion struct {
	LibraryID string `json:"library_id"`
	Name      string `json:"name"`
	// Tier is the default tier stored now (null: none).
	Tier *int `json:"tier"`
	// SuggestedTier is what the form starts with for a library with no tier: 0 (All
	// ages) or null (Not rated).
	SuggestedTier *int `json:"suggested_tier"`
	// Classified is how many of its videos have a content type; Lectures how many of
	// those are lectures.
	Classified int `json:"classified"`
	Lectures   int `json:"lectures"`
}

// LibrarySuggestions lists every library with the tier Rate libraries starts with.
func (s *Store) LibrarySuggestions(ctx context.Context) ([]LibrarySuggestion, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT l.id::text, l.name, l.age_tier, COALESCE(v.content_type, ''), count(v.id)
		   FROM libraries l
		   LEFT JOIN videos v ON v.library_id = l.id AND v.content_type IS NOT NULL
		  GROUP BY l.id, l.name, l.age_tier, COALESCE(v.content_type, '')
		  ORDER BY lower(l.name), l.id`)
	if err != nil {
		return nil, fmt.Errorf("parental: library suggestions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []LibrarySuggestion{}
	counts := map[string]map[string]int{}
	for rows.Next() {
		var (
			id, name, typ string
			tier          sql.NullInt64
			n             int
		)
		if err := rows.Scan(&id, &name, &tier, &typ, &n); err != nil {
			return nil, err
		}
		if counts[id] == nil {
			counts[id] = map[string]int{}
			out = append(out, LibrarySuggestion{LibraryID: id, Name: name, Tier: intPtr(tier)})
		}
		counts[id][typ] += n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		for typ, n := range counts[out[i].LibraryID] {
			if typ == "" || typ == "unknown" {
				continue
			}
			out[i].Classified += n
			if lectureTypes[typ] {
				out[i].Lectures += n
			}
		}
		out[i].SuggestedTier = SuggestTier(counts[out[i].LibraryID])
	}
	return out, nil
}
