package main

import "strings"

// ssdpOn is whether the TV front door answers SSDP and HDHomeRun discovery: off when the
// supervisor says so (MAKTABA_DISCOVERY_SSDP=0, a container on a Docker bridge, where
// multicast and broadcast reach no device: Epic 59 story 59.08). Plex and a TV still reach
// the tuner by its address.
func ssdpOn(getenv func(string) string) bool {
	v := strings.TrimSpace(strings.ToLower(getenv("MAKTABA_DISCOVERY_SSDP")))
	return v != "0" && v != "false" && v != "off"
}
