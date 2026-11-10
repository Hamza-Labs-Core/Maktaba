package supervisor

import (
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// The pipeline's quiet-hours environment (Epic 59 story 59.02, engine/quiet.py).
const (
	EnvQuietHours   = "MAKTABA_QUIET_HOURS"
	EnvQuietSprites = "MAKTABA_QUIET_SPRITES"
)

// QuietEnv is the window heavy work waits for on this machine ([schedule] quiet_hours,
// else the hardware tier's) and, on Light, that thumbnail sprites wait for it too. None
// when there is no window.
func QuietEnv(cfg config.Config) []string {
	tier, _ := hostinfo.ParseTier(cfg.Server.Tier)
	d := hostinfo.DefaultsFor(tier, hostinfo.Machine{})
	if tier == "" {
		d.QuietHours, d.SpritesInQuietHours = "", false // no tier read: today's (none)
	}
	w := config.QuietHoursOf(cfg, d.QuietHours)
	if w == "" {
		return nil
	}
	out := []string{EnvQuietHours + "=" + w}
	if d.SpritesInQuietHours {
		out = append(out, EnvQuietSprites+"=1")
	}
	return out
}
