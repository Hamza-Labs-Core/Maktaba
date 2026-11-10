package supervisor

import (
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 59 story 59.02: Light and Standard wait for 01:00-06:00, Light's sprites too;
// Strong none; [schedule] quiet_hours wins, "" meaning none.
func TestQuietEnv(t *testing.T) {
	cfg := config.Defaults()
	for tier, want := range map[string][]string{
		"light":    {"MAKTABA_QUIET_HOURS=01:00-06:00", "MAKTABA_QUIET_SPRITES=1"},
		"standard": {"MAKTABA_QUIET_HOURS=01:00-06:00"},
		"strong":   nil,
		"":         nil,
	} {
		c := cfg
		c.Server.Tier = tier
		if got := QuietEnv(c); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", tier, got, want)
		}
	}
	none, late := "", "22:00-06:00"
	c := cfg
	c.Server.Tier = "standard"
	c.Schedule.QuietHours = &none
	if got := QuietEnv(c); got != nil {
		t.Errorf("none: %v", got)
	}
	c.Server.Tier = "strong"
	c.Schedule.QuietHours = &late
	if got := QuietEnv(c); !slices.Equal(got, []string{"MAKTABA_QUIET_HOURS=22:00-06:00"}) {
		t.Errorf("strong with a window: %v", got)
	}
}
