package main

import (
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/componentseed"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// choiceVenv are the extras the boot's venv gate builds with for setup's bundle choice
// (story 51.17): ok is false, today's default, unless setup chose Core or a Custom set
// without the ML runtime, and the ML runtime has not come since (its pack, or a desired
// state that wants it: the admin added it after setup).
func choiceVenv(cfg config.Config, pl packs.Layout) (extras string, none, ok bool) {
	ch, found, err := componentseed.ReadChoice(cfg.StateDir())
	if err != nil || !found {
		return "", false, false
	}
	extras, none = ch.VenvExtras()
	if extras == "" && !none {
		return "", false, false
	}
	if pl.Current("runtime.ml") != "" {
		return "", false, false
	}
	if d, has := supervisor.CachedDesired(cfg.StateDir(), componentsMachine(cfg)); has {
		for _, id := range []string{"runtime.ml", "tool.yt-dlp"} {
			if w, ok := d[id]; ok && w.Want != components.WantAbsent && !ch.Has(id) {
				return "", false, false
			}
		}
	}
	return extras, none, true
}
