package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Every key unset is the behaviour from before [backups]; a default chosen again is
// written as unset.
func TestBackupSettingsRoundTrip(t *testing.T) {
	def := BackupsSection{}.Settings()
	want := BackupSettings{Nightly: true, EveryDays: 7, Hour: 3, Keep: 4, KeepMonthly: 12, KeepYearly: 5, CompressAfterDays: 60, PreUpdate: true, ReservePercent: 5,
		ReserveMinGB: 2, MaxSharePercent: 20}
	if def != want {
		t.Fatalf("defaults %+v", def)
	}
	if !reflect.DeepEqual(def.Section(), BackupsSection{}) {
		t.Errorf("the defaults write %+v", def.Section())
	}
	s := BackupSettings{Dir: "/Volumes/Big/backups", Nightly: false, EveryDays: 7, Hour: 0, Keep: 14, UpdateKeep: 2,
		PreUpdate: false, ReservePercent: 0, ReserveMinGB: 0, MaxSharePercent: 40}
	sec := s.Section()
	if sec.KeepMonthly == nil || *sec.KeepMonthly != 0 || sec.KeepYearly == nil || *sec.KeepYearly != 0 || sec.CompressAfterDays == nil || *sec.CompressAfterDays != 0 || sec.ReservePercent != -1 || sec.ReserveMinGB != -1 || sec.Hour == nil || *sec.Hour != 0 {
		t.Errorf("section %+v", sec)
	}
	if got := sec.Settings(); got != s {
		t.Errorf("round trip %+v, want %+v", got, s)
	}
	if sec.ReserveShare() != 0 || sec.ReserveMinBytes() != 0 || sec.MaxShare() != 0.4 || sec.NightlyOn() || sec.PreUpdateOn() {
		t.Errorf("accessors of %+v", sec)
	}
}

func TestBackupsSavedAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.toml")
	cfg := Defaults()
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "backups") {
		t.Errorf("a default config writes [backups]:\n%s", b)
	}
	cfg.Backups = BackupSettings{Dir: "/b", Nightly: true, EveryDays: 2, Hour: 0, Keep: 3, KeepMonthly: 6, CompressAfterDays: 90, PreUpdate: true,
		ReservePercent: 5, ReserveMinGB: 2, MaxSharePercent: 20}.Section()
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	got, _, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := got.Backups.Settings(); s.Dir != "/b" || s.EveryDays != 2 || s.Hour != 0 || s.Keep != 3 || s.KeepMonthly != 6 || s.KeepYearly != 0 || s.CompressAfterDays != 90 {
		t.Errorf("read back %+v", s)
	}
	// the folder as this OS writes it (BackupsDir cleans it: a backslash on Windows)
	if got.BackupsDir("/state/backups") != filepath.Clean("/b") || (Config{}).BackupsDir("/state/backups") != "/state/backups" {
		t.Error("BackupsDir")
	}
}

func TestBackupPatchAndValidate(t *testing.T) {
	s := BackupsSection{}.Settings()
	h, keep, dir := 25, 0, "rel/dir"
	changed := BackupPatch{Hour: &h, Keep: &keep, Dir: &dir}.Apply(&s)
	if strings.Join(changed, ",") != "dir,hour,keep" {
		t.Errorf("changed %v", changed)
	}
	errs := ValidateBackups(s)
	var fields []string
	for _, e := range errs {
		fields = append(fields, e.Field)
	}
	if strings.Join(fields, ",") != "backups.dir,backups.hour,backups.keep" {
		t.Errorf("errors %v", errs)
	}
	if errs := ValidateBackups(BackupsSection{}.Settings()); len(errs) != 0 {
		t.Errorf("the defaults are refused: %v", errs)
	}
}

func TestArchivePolicyBounds(t *testing.T) {
	s := BackupsSection{}.Settings()
	s.KeepMonthly, s.KeepYearly, s.CompressAfterDays = 120, 50, 3650
	if errs := ValidateBackups(s); len(errs) != 0 {
		t.Fatal(errs)
	}
	s.KeepMonthly, s.KeepYearly, s.CompressAfterDays = 121, -1, 3651
	if errs := ValidateBackups(s); len(errs) != 3 {
		t.Fatal(errs)
	}
}

func TestArchivePolicyTOMLZeroAndLegacyOff(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.toml")
			raw := "[backups]\nkeep_monthly = " + value + "\nkeep_yearly = " + value + "\ncompress_after_days = " + value + "\n"
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, _, err := LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			settings := cfg.Backups.Settings()
			if settings.KeepMonthly != 0 || settings.KeepYearly != 0 || settings.CompressAfterDays != 0 {
				t.Fatal(settings)
			}
			cfg.Backups = settings.Section()
			if err := cfg.SaveTo(path); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"keep_monthly", "keep_yearly", "compress_after_days"} {
				if !strings.Contains(string(saved), key+" = 0") {
					t.Errorf("%s missing explicit zero in %s", key, saved)
				}
			}
		})
	}
}
