package main

// The container's network (Epic 59 story 59.08). On a Docker bridge multicast never leaves
// the bridge: no app finds the home by mDNS and no TV by SSDP, so both are off, and the
// log says how apps still connect (its address, its HTTPS name, a code from Maktaba
// Cloud), in English and Arabic, since the container log is what a NAS shows first.

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// The variables the roles read: the API's mDNS (api/cluster_bootstrap.go) and
// streaming's SSDP and HDHomeRun discovery.
const (
	envDiscoveryMDNS = "MAKTABA_DISCOVERY_MDNS"
	envDiscoverySSDP = "MAKTABA_DISCOVERY_SSDP"
)

// onBridge is whether this machine is a container on a Docker bridge (tests replace it).
var onBridge = func() bool { return detectMachine().Network.Bridge() }

// bridgeText is setup.check.note.network_bridge in both languages, {url} filled.
func bridgeText(url string) (en, ar string) {
	t := webSetupTexts["setup.check.note.network_bridge"]
	return strings.ReplaceAll(t[0], "{url}", url), strings.ReplaceAll(t[1], "{url}", url)
}

// bridgeOff turns discovery off for the roles on a bridge and says so; elsewhere it does
// nothing.
func bridgeOff(n hostinfo.Network, webURL string, log io.Writer) {
	if !n.Bridge() {
		return
	}
	_ = os.Setenv(envDiscoveryMDNS, "0")
	_ = os.Setenv(envDiscoverySSDP, "0")
	en, ar := bridgeText(webURL)
	fmt.Fprintf(log, "[serve] network: bridge (%s, %s): mDNS and SSDP off\n[serve] %s\n[serve] %s\n",
		orQuestion(n.Interface), orQuestion(n.Driver), en, ar)
}

func orQuestion(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
