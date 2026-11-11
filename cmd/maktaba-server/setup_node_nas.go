package main

// Epic 59 story 59.13: a NAS as a full node of a home elsewhere. Standard and Strong NAS
// boxes may join as nodes; a Light one is better as the home or as storage, so joining
// says why and needs the household's explicit choice (--light-nas). Nothing hard-blocks.

import (
	"errors"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// LightNASCode is the warning's code (cluster.join.light_nas in the apps).
const LightNASCode = "light_nas"

// LightNASText is its English.
const LightNASText = "A Light NAS should be the Maktaba home or just storage. As a node it would slow the NAS " +
	"down without helping the home much."

// ErrLightNAS: setup --node on a Light NAS without --light-nas.
var ErrLightNAS = errors.New(LightNASText + " To join it anyway, run setup --node again with --light-nas.")

// lightNAS reports whether m, with the tier cfg's [server] sets, is a NAS on the Light
// tier (the admin's pick wins, as for every tier default).
func lightNAS(cfg config.ServerSection, m hostinfo.Machine) bool {
	return hostinfo.IsNAS(m.Platform) && tierOf(cfg, m).Tier == hostinfo.TierLight
}
