package ports

import (
	"os"
	"strings"
)

// Outbound calls (#640). Every call a Maktaba process makes to another machine on the
// internet is listed in Outbound, with the switch that stops it. MAKTABA_OFFLINE=1 (what
// `maktaba-server serve --offline` sets for itself and every role) stops each one a
// server makes by itself (By "auto"): a test stack, a branch's scratch stack or an offline
// home then calls nothing outside on its own. The ones an admin starts (By "admin": a Plex
// sign-in, a web search) run only when an admin acts; a pack install is refused while
// packs are off. TestOutboundHostsAreListed fails on a host
// in the code that is not listed here, so a new call cannot be added silently.

// EnvOffline turns every outbound call off.
const EnvOffline = "MAKTABA_OFFLINE"

// Offline reports whether MAKTABA_OFFLINE is on ("1", "true", "on", "yes").
func Offline() bool { return OfflineFrom(os.Getenv) }

// OfflineFrom is Offline with getenv (tests, a handler's own environment).
func OfflineFrom(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvOffline))) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// Call is one kind of outbound call.
type Call struct {
	// Name is the call's short name.
	Name string
	// Hosts are the hosts it reaches (as the code names them; a configured URL is "").
	Hosts []string
	// By is what makes it: auto (the server, on its own) or admin (an admin's action).
	By string
	// What it is for.
	What string
	// Off is what turns it off besides MAKTABA_OFFLINE.
	Off string
}

// Outbound is every outbound call, in no particular order.
var Outbound = []Call{
	{Name: "updates", Hosts: []string{"api.github.com", "github.com"}, By: "auto",
		What: "the update check, the release download and the self-update (GitHub Releases)",
		Off:  "MAKTABA_UPDATE_DISABLE=1"},
	{Name: "sigstore-tuf", Hosts: []string{"tuf-repo-cdn.sigstore.dev"}, By: "auto",
		What: "Sigstore's trusted root, refreshed for release verification and passed on to the machines",
		Off:  "MAKTABA_UPDATE_DISABLE=1"},
	{Name: "packs", Hosts: []string{"github.com", "huggingface.co"}, By: "auto",
		What: "component packs and models (Epic 51 story 51.16)",
		Off:  `[components] packs = "off" (MAKTABA_PACKS_URL retargets it)`},
	{Name: "llm-models", Hosts: []string{"registry.ollama.ai", "ollama.com"}, By: "auto",
		What: "the language model the bundled Ollama pulls", Off: `[llm] pull = "off"`},
	{Name: "telemetry", Hosts: []string{"api.maktaba.tv"}, By: "auto",
		What: "anonymous usage data to Maktaba Cloud (Epic 16)",
		Off:  `[telemetry] enabled = "off", MAKTABA_TELEMETRY_URL=off`},
	{Name: "cloud-licence", Hosts: []string{"api.maktaba.tv"}, By: "auto",
		What: "the licence refresh from Maktaba Cloud", Off: "no cloud link (MAKTABA_CLOUD_IDENTITY unset)"},
	{Name: "cloud-link", Hosts: []string{"api.maktaba.tv"}, By: "auto",
		What: "the relay agent's link to Maktaba Cloud (remote access)", Off: "[cloud] enabled = false"},
	{Name: "postgres-binaries", Hosts: []string{"github.com"}, By: "admin",
		What: "setup's download of Postgres for a machine", Off: "setup only"},
	{Name: "huggingface-setup", Hosts: []string{"huggingface.co"}, By: "admin",
		What: "setup's model download", Off: "setup only"},
	{Name: "plex-tv", Hosts: []string{"plex.tv", "app.plex.tv"}, By: "admin",
		What: "a Plex sign-in", Off: "never started by the server"},
	{Name: "youtube", Hosts: []string{"www.youtube.com", "i.ytimg.com"}, By: "admin",
		What: "YouTube links, thumbnails and downloads of the libraries an admin linked", Off: "no YouTube source"},
	{Name: "websearch", Hosts: []string{"www.googleapis.com"}, By: "admin",
		What: "enrichment's web search, when an admin set a search key", Off: "no search key"},
	{Name: "cloudflare", Hosts: []string{"api.cloudflare.com"}, By: "admin",
		What: "the TV remote's Cloudflare Stream, when an admin set it up", Off: "ext.cloudflare_stream off"},
}

// OutboundHosts are the listed hosts.
func OutboundHosts() map[string]bool {
	out := map[string]bool{}
	for _, c := range Outbound {
		for _, h := range c.Hosts {
			if h != "" {
				out[h] = true
			}
		}
	}
	return out
}
