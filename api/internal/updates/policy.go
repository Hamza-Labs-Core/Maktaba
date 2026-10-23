package updates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SettingsKey is the app_settings row of the rules.updates section (the rules handler's
// "rules." prefix + the section key).
const SettingsKey = "rules.updates"

// Section is the rules section name.
const Section = "updates"

// autoInstallEnabled is the build switch of plan 35.3 §4: phase B (the home's update
// orchestrator, stories 35.4-35.7, and the rollout, 35.12) has shipped, so automatic
// installs follow rules.updates.auto_install (default on). A build can still turn them
// off at link time with
//
//	-ldflags "-X github.com/Hamza-Labs-Core/Maktaba/api/internal/updates.autoInstallEnabled=false"
var autoInstallEnabled = "true"

// AutoInstallPendingMessage is what Update now answers while AutoInstallAvailable is
// false (the web shows the same text from updates.settings.notBuilt).
const AutoInstallPendingMessage = "Automatic install arrives with the update orchestrator"

// AutoInstallAvailable reports whether this build may install releases by itself.
func AutoInstallAvailable() bool { return autoInstallEnabled == "true" }

// Channels.
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// Node failure policies (README decision "Machine failure").
const (
	FailureHoldPlatform = "hold_platform"
	FailureHalt         = "halt"
	FailureContinue     = "continue"
)

// Policy is rules.updates (README "Settings (rules.updates)"), with the MAKTABA_UPDATE_*
// environment as the base and the settings page's changes over it.
type Policy struct {
	AutoInstall              bool       `json:"auto_install"`
	Channel                  string     `json:"channel"`
	Window                   string     `json:"window"`
	WindowOverrunMinutes     int        `json:"window_overrun_minutes"`
	MinReleaseAgeHours       int        `json:"min_release_age_hours"`
	PinnedVersion            string     `json:"pinned_version"`
	DeferredUntil            *time.Time `json:"deferred_until"`
	SkippedVersions          []string   `json:"skipped_versions"`
	NodeFailurePolicy        string     `json:"node_failure_policy"`
	NodeHealthTimeoutMinutes int        `json:"node_health_timeout_minutes"`
	DrainTimeoutMinutes      int        `json:"drain_timeout_minutes"`
	RehearseMigrations       bool       `json:"rehearse_migrations"`
	RehearseMaxDBGB          int        `json:"rehearse_max_db_gb"`
	KeepVersions             int        `json:"keep_versions"`
	BackupsKeep              int        `json:"backups_keep"`
	CatchUp                  string     `json:"catch_up"`
	ProxyCacheMB             int        `json:"proxy_cache_mb"`
}

// DefaultPolicy is the README defaults (auto_install true: owner decision).
func DefaultPolicy() Policy {
	return Policy{
		AutoInstall:              true,
		Channel:                  ChannelStable,
		Window:                   "03:00-05:00",
		WindowOverrunMinutes:     120,
		MinReleaseAgeHours:       24,
		SkippedVersions:          []string{},
		NodeFailurePolicy:        FailureHoldPlatform,
		NodeHealthTimeoutMinutes: 10,
		DrainTimeoutMinutes:      10,
		RehearseMigrations:       true,
		RehearseMaxDBGB:          20,
		KeepVersions:             3,
		BackupsKeep:              3,
		CatchUp:                  "immediate",
		ProxyCacheMB:             2048,
	}
}

// EffectiveAutoInstall is auto_install as this build applies it (phase A: off).
func (p Policy) EffectiveAutoInstall() bool { return p.AutoInstall && AutoInstallAvailable() }

// Skipped reports whether v is in skipped_versions.
func (p Policy) Skipped(v string) bool {
	for _, s := range p.SkippedVersions {
		if CompareVersions(s, v) == 0 && NormalizeVersion(s) != "" {
			return true
		}
	}
	return false
}

// ApplyEnv layers the MAKTABA_UPDATE_* environment over p (the base under the settings
// page): MAKTABA_UPDATE_CHANNEL, MAKTABA_UPDATE_AUTO_INSTALL, MAKTABA_UPDATE_WINDOW,
// MAKTABA_UPDATE_MIN_AGE_HOURS, MAKTABA_UPDATE_PIN. Unusable values are ignored.
func (p Policy) ApplyEnv(getenv func(string) string) Policy {
	if getenv == nil {
		return p
	}
	if v := strings.ToLower(strings.TrimSpace(getenv("MAKTABA_UPDATE_CHANNEL"))); v == ChannelStable || v == ChannelBeta {
		p.Channel = v
	}
	if v := strings.TrimSpace(getenv("MAKTABA_UPDATE_AUTO_INSTALL")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			p.AutoInstall = b
		}
	}
	if v := strings.TrimSpace(getenv("MAKTABA_UPDATE_WINDOW")); v != "" {
		if _, err := ParseWindow(v); err == nil {
			p.Window = v
		}
	}
	if v := strings.TrimSpace(getenv("MAKTABA_UPDATE_MIN_AGE_HOURS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			p.MinReleaseAgeHours = n
		}
	}
	if v := strings.TrimSpace(getenv("MAKTABA_UPDATE_PIN")); v != "" && ValidVersion(v) {
		p.PinnedVersion = NormalizeVersion(v)
	}
	return p
}

// ApplyOverrides layers the stored rules.updates changes (only the settings that differ
// from their defaults, as the rules handler stores them) over p. A value of the wrong
// shape is skipped (the rules handler validated it on save).
func (p Policy) ApplyOverrides(o map[string]json.RawMessage) Policy {
	b := func(name string, dst *bool) {
		if raw, ok := o[name]; ok {
			var v bool
			if json.Unmarshal(raw, &v) == nil {
				*dst = v
			}
		}
	}
	i := func(name string, dst *int) {
		if raw, ok := o[name]; ok {
			var v float64
			if json.Unmarshal(raw, &v) == nil {
				*dst = int(v)
			}
		}
	}
	s := func(name string, dst *string) {
		if raw, ok := o[name]; ok {
			var v string
			if json.Unmarshal(raw, &v) == nil {
				*dst = strings.TrimSpace(v)
			}
		}
	}
	b("auto_install", &p.AutoInstall)
	s("channel", &p.Channel)
	s("window", &p.Window)
	i("window_overrun_minutes", &p.WindowOverrunMinutes)
	i("min_release_age_hours", &p.MinReleaseAgeHours)
	s("pinned_version", &p.PinnedVersion)
	p.PinnedVersion = NormalizeVersion(p.PinnedVersion)
	if raw, ok := o["deferred_until"]; ok {
		var v string
		if json.Unmarshal(raw, &v) == nil {
			p.DeferredUntil = nil
			if t, err := ParseInstant(v); err == nil && !t.IsZero() {
				p.DeferredUntil = &t
			}
		}
	}
	if raw, ok := o["skipped_versions"]; ok {
		var v []string
		if json.Unmarshal(raw, &v) == nil {
			p.SkippedVersions = v
		}
	}
	s("node_failure_policy", &p.NodeFailurePolicy)
	i("node_health_timeout_minutes", &p.NodeHealthTimeoutMinutes)
	i("drain_timeout_minutes", &p.DrainTimeoutMinutes)
	b("rehearse_migrations", &p.RehearseMigrations)
	i("rehearse_max_db_gb", &p.RehearseMaxDBGB)
	i("keep_versions", &p.KeepVersions)
	i("backups_keep", &p.BackupsKeep)
	s("catch_up", &p.CatchUp)
	i("proxy_cache_mb", &p.ProxyCacheMB)
	if p.SkippedVersions == nil {
		p.SkippedVersions = []string{}
	}
	return p
}

// ParseInstant reads deferred_until: RFC 3339, or "" for none.
func ParseInstant(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

// Policy errors (400 codes on the settings and skip/defer endpoints).
var (
	ErrPinBelowInstalled = errors.New("pin_below_installed")
	ErrBadPin            = errors.New("bad_pin")
	ErrBadWindow         = errors.New("bad_window")
	ErrBadChannel        = errors.New("bad_channel")
	ErrBadDeferral       = errors.New("bad_deferral")
)

// Validate checks p; installed is the watermark (the highest version committed), a pin
// below it is refused (story 35.3 AC8).
func (p Policy) Validate(installed string) error {
	if p.Channel != ChannelStable && p.Channel != ChannelBeta {
		return fmt.Errorf("%w: %q", ErrBadChannel, p.Channel)
	}
	if _, err := ParseWindow(p.Window); err != nil {
		return err
	}
	if p.PinnedVersion != "" {
		if !ValidVersion(p.PinnedVersion) {
			return fmt.Errorf("%w: %q", ErrBadPin, p.PinnedVersion)
		}
		if installed != "" && CompareVersions(p.PinnedVersion, installed) < 0 {
			return fmt.Errorf("%w: %s is below the installed %s", ErrPinBelowInstalled, p.PinnedVersion, installed)
		}
	}
	return nil
}

// LoadOverrides reads the stored rules.updates changes (none: an empty map). A missing
// app_settings table (a partial test database) is no changes.
func LoadOverrides(ctx context.Context, db *sql.DB) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if db == nil {
		return out, nil
	}
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, SettingsKey).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return out, nil
	case err != nil:
		if strings.Contains(strings.ToLower(err.Error()), "app_settings") {
			return out, nil
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return map[string]json.RawMessage{}, nil
	}
	return out, nil
}

// LoadPolicy is the effective policy: defaults, base channel, environment, settings.
func LoadPolicy(ctx context.Context, db *sql.DB, baseChannel string, getenv func(string) string) (Policy, error) {
	p := DefaultPolicy()
	if c := strings.ToLower(strings.TrimSpace(baseChannel)); c == ChannelBeta {
		p.Channel = c
	}
	p = p.ApplyEnv(getenv)
	o, err := LoadOverrides(ctx, db)
	if err != nil {
		return p, err
	}
	return p.ApplyOverrides(o), nil
}
