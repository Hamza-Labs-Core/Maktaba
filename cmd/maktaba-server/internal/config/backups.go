package config

// [backups] in server.toml: where the library database's dumps go and how many stay
// (Epic 36 story 36.16). The server reads it before its database step (the nightly
// dumps, the dump before an update's migrations, the room they leave on their disk);
// an admin changes it from Settings → Storage (the supervisor's /config/backups),
// setup's storage step or `maktaba-server backups set`. Every key unset keeps the
// behaviour from before it: <state>/backups, a nightly dump from 03:00 local time,
// the newest 7 kept, the pre-update dump taken, 5% of the disk (at least 2 GiB)
// left free and the dumps within a fifth of it.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// BackupsSection is [backups].
type BackupsSection struct {
	// Dir is the backups folder ("": <state>/backups, beside the database).
	Dir string `toml:"dir,omitempty"`
	// Nightly is "off" to take no scheduled dump ("" or "on": taken).
	Nightly string `toml:"nightly,omitempty"`
	// EveryDays is how many days apart the scheduled dumps are (0: 1, daily; 7: weekly).
	EveryDays int `toml:"every_days,omitzero"`
	// Hour is the local hour from which a scheduled dump is due (nil: 3).
	Hour *int `toml:"hour,omitempty"`
	// Keep is how many scheduled dumps stay (0: 7).
	Keep int `toml:"keep,omitzero"`
	// UpdateKeep is how many pre-update dumps stay (0: rules.updates.backups_keep as the
	// last update recorded it, else [postgres] update_dumps).
	UpdateKeep int `toml:"update_keep,omitzero"`
	// PreUpdate is "off" to take no dump before an update's migrations: an update whose
	// migrations fail then cannot put the database back.
	PreUpdate string `toml:"pre_update,omitempty"`
	// ReservePercent is the share of the backups' disk a dump leaves free (0: 5; -1: none).
	ReservePercent int `toml:"reserve_percent,omitzero"`
	// ReserveMinGB is the least it leaves free, in GiB (0: 2; -1: none).
	ReserveMinGB int `toml:"reserve_min_gb,omitzero"`
	// MaxSharePercent is the most of the disk the dumps take together (0: 20).
	MaxSharePercent int `toml:"max_share_percent,omitzero"`
}

// The defaults of [backups].
const (
	DefaultBackupHour       = 3
	DefaultBackupKeep       = 7
	DefaultReservePercent   = 5
	DefaultReserveMinGB     = 2
	DefaultMaxSharePercent  = 20
	DefaultBackupsEveryDays = 1
)

// NightlyOn: the scheduled dump is taken.
func (b BackupsSection) NightlyOn() bool {
	return !strings.EqualFold(strings.TrimSpace(b.Nightly), "off")
}

// PreUpdateOn: a dump is taken before an update's migrations.
func (b BackupsSection) PreUpdateOn() bool {
	return !strings.EqualFold(strings.TrimSpace(b.PreUpdate), "off")
}

// EveryDaysOr is EveryDays, 1 when unset.
func (b BackupsSection) EveryDaysOr() int {
	if b.EveryDays < 1 {
		return DefaultBackupsEveryDays
	}
	return b.EveryDays
}

// HourOr is Hour, DefaultBackupHour when unset or out of range.
func (b BackupsSection) HourOr() int {
	if b.Hour == nil || *b.Hour < 0 || *b.Hour > 23 {
		return DefaultBackupHour
	}
	return *b.Hour
}

// KeepOr is Keep, DefaultBackupKeep when unset.
func (b BackupsSection) KeepOr() int {
	if b.Keep < 1 {
		return DefaultBackupKeep
	}
	return b.Keep
}

// ReserveShare is the share of the disk a dump leaves free (0..1).
func (b BackupsSection) ReserveShare() float64 {
	switch {
	case b.ReservePercent < 0:
		return 0
	case b.ReservePercent == 0:
		return float64(DefaultReservePercent) / 100
	}
	return float64(b.ReservePercent) / 100
}

// ReserveMinBytes is the least a dump leaves free.
func (b BackupsSection) ReserveMinBytes() uint64 {
	switch {
	case b.ReserveMinGB < 0:
		return 0
	case b.ReserveMinGB == 0:
		return DefaultReserveMinGB << 30
	}
	return uint64(b.ReserveMinGB) << 30
}

// MaxShare is the most of the disk the dumps take together (0..1).
func (b BackupsSection) MaxShare() float64 {
	if b.MaxSharePercent < 1 {
		return float64(DefaultMaxSharePercent) / 100
	}
	return float64(b.MaxSharePercent) / 100
}

// BackupsDir is where the library database's dumps go: [backups] dir, else def (the
// install's own place: <state>/backups for the bundled Postgres).
func (c Config) BackupsDir(def string) string {
	if d := strings.TrimSpace(c.Backups.Dir); d != "" {
		return filepath.Clean(d)
	}
	return def
}

// DefaultBackupsDir is <state>/backups.
func (c Config) DefaultBackupsDir() string { return filepath.Join(c.StateDir(), "backups") }

// BackupSettings are [backups] as the page and the command show them: every value
// as it applies (the defaults filled in).
type BackupSettings struct {
	// Dir is the backups folder ("": beside the database, DefaultBackupsDir).
	Dir       string `json:"dir"`
	Nightly   bool   `json:"nightly"`
	EveryDays int    `json:"every_days"`
	Hour      int    `json:"hour"`
	Keep      int    `json:"keep"`
	// UpdateKeep: 0 follows rules.updates.backups_keep.
	UpdateKeep      int  `json:"update_keep"`
	PreUpdate       bool `json:"pre_update"`
	ReservePercent  int  `json:"reserve_percent"`
	ReserveMinGB    int  `json:"reserve_min_gb"`
	MaxSharePercent int  `json:"max_share_percent"`
}

// Settings is b with the defaults filled in.
func (b BackupsSection) Settings() BackupSettings {
	pct := func(v, def int) int {
		switch {
		case v < 0:
			return 0
		case v == 0:
			return def
		}
		return v
	}
	share := b.MaxSharePercent
	if share < 1 {
		share = DefaultMaxSharePercent
	}
	return BackupSettings{Dir: strings.TrimSpace(b.Dir), Nightly: b.NightlyOn(), EveryDays: b.EveryDaysOr(),
		Hour: b.HourOr(), Keep: b.KeepOr(), UpdateKeep: b.UpdateKeep, PreUpdate: b.PreUpdateOn(),
		ReservePercent: pct(b.ReservePercent, DefaultReservePercent), ReserveMinGB: pct(b.ReserveMinGB, DefaultReserveMinGB),
		MaxSharePercent: share}
}

// Section is s as [backups] writes it: a default left unset.
func (s BackupSettings) Section() BackupsSection {
	b := BackupsSection{Dir: strings.TrimSpace(s.Dir), UpdateKeep: s.UpdateKeep}
	if !s.Nightly {
		b.Nightly = "off"
	}
	if !s.PreUpdate {
		b.PreUpdate = "off"
	}
	if s.EveryDays != DefaultBackupsEveryDays {
		b.EveryDays = s.EveryDays
	}
	if s.Hour != DefaultBackupHour {
		h := s.Hour
		b.Hour = &h
	}
	if s.Keep != DefaultBackupKeep {
		b.Keep = s.Keep
	}
	pct := func(v, def int) int {
		switch v {
		case def:
			return 0
		case 0:
			return -1
		}
		return v
	}
	b.ReservePercent = pct(s.ReservePercent, DefaultReservePercent)
	b.ReserveMinGB = pct(s.ReserveMinGB, DefaultReserveMinGB)
	if s.MaxSharePercent != DefaultMaxSharePercent {
		b.MaxSharePercent = s.MaxSharePercent
	}
	return b
}

// BackupPatch is a change to some of the settings (a key left out stays).
type BackupPatch struct {
	Dir             *string `json:"dir"`
	Nightly         *bool   `json:"nightly"`
	EveryDays       *int    `json:"every_days"`
	Hour            *int    `json:"hour"`
	Keep            *int    `json:"keep"`
	UpdateKeep      *int    `json:"update_keep"`
	PreUpdate       *bool   `json:"pre_update"`
	ReservePercent  *int    `json:"reserve_percent"`
	ReserveMinGB    *int    `json:"reserve_min_gb"`
	MaxSharePercent *int    `json:"max_share_percent"`
}

// Apply changes s by p and lists the keys it changed.
func (p BackupPatch) Apply(s *BackupSettings) []string {
	var changed []string
	str := func(key string, v *string, dst *string) {
		if v != nil && strings.TrimSpace(*v) != *dst {
			*dst = strings.TrimSpace(*v)
			changed = append(changed, key)
		}
	}
	boolean := func(key string, v *bool, dst *bool) {
		if v != nil && *v != *dst {
			*dst = *v
			changed = append(changed, key)
		}
	}
	integer := func(key string, v *int, dst *int) {
		if v != nil && *v != *dst {
			*dst = *v
			changed = append(changed, key)
		}
	}
	str("dir", p.Dir, &s.Dir)
	boolean("nightly", p.Nightly, &s.Nightly)
	integer("every_days", p.EveryDays, &s.EveryDays)
	integer("hour", p.Hour, &s.Hour)
	integer("keep", p.Keep, &s.Keep)
	integer("update_keep", p.UpdateKeep, &s.UpdateKeep)
	boolean("pre_update", p.PreUpdate, &s.PreUpdate)
	integer("reserve_percent", p.ReservePercent, &s.ReservePercent)
	integer("reserve_min_gb", p.ReserveMinGB, &s.ReserveMinGB)
	integer("max_share_percent", p.MaxSharePercent, &s.MaxSharePercent)
	return changed
}

// ValidateBackups checks s's values (not the folder's disk: the supervisor and the
// command do, with the disk at hand). Field names are "backups.<key>".
func ValidateBackups(s BackupSettings) []FieldError {
	var errs []FieldError
	bad := func(key, format string, a ...any) {
		errs = append(errs, FieldError{Field: "backups." + key, Message: fmt.Sprintf(format, a...)})
	}
	if s.Dir != "" && !filepath.IsAbs(s.Dir) {
		bad("dir", "the backups folder must be a whole path")
	}
	if s.EveryDays < 1 || s.EveryDays > 30 {
		bad("every_days", "a dump every 1 to 30 days")
	}
	if s.Hour < 0 || s.Hour > 23 {
		bad("hour", "the hour is 0 to 23")
	}
	if s.Keep < 1 || s.Keep > 365 {
		bad("keep", "keep 1 to 365 scheduled dumps")
	}
	if s.UpdateKeep < 0 || s.UpdateKeep > 50 {
		bad("update_keep", "keep 1 to 50 pre-update dumps (0: as the update settings say)")
	}
	if s.ReservePercent < 0 || s.ReservePercent > 50 {
		bad("reserve_percent", "leave 0 to 50 percent of the disk free")
	}
	if s.ReserveMinGB < 0 || s.ReserveMinGB > 1000 {
		bad("reserve_min_gb", "leave 0 to 1000 GB free")
	}
	if s.MaxSharePercent < 1 || s.MaxSharePercent > 100 {
		bad("max_share_percent", "the dumps take 1 to 100 percent of the disk")
	}
	return errs
}
