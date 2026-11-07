package ports

import (
	"os"
	"strings"
)

// Outbound calls (#640). Every call a Maktaba process makes to another machine on the
// internet is listed in Outbound, with the switch that stops it. MAKTABA_OFFLINE=1 (what
// `maktaba-server serve --offline` sets for itself and every role) stops each one a
// server makes by itself (By "auto"): a test stack, a branch's scratch stack or an offline
// home then calls nothing outside on its own; the pipeline reads the variable too
// (maktaba_pipeline/offline.py). The ones an admin starts (By "admin": a Plex sign-in, a
// machine's conversion) run only when an admin acts; a pack install is refused while packs
// are off. TestOutboundHostsAreListed fails on a host in the Go or the pipeline's Python
// code that is not listed here, so a new call cannot be added silently.

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
	{Name: "youtube", Hosts: []string{"www.youtube.com", "i.ytimg.com"}, By: "auto",
		What: "the listings, downloads, exact dates and repairs of the libraries an admin linked to YouTube; " +
			"the reconciler refreshes their listings by itself (the pipeline's YouTube stages)",
		Off: "no YouTube source; offline, the stages are not claimed and no refresh is queued"},
	{Name: "encyclopedia", Hosts: []string{"ar.wikipedia.org"}, By: "auto",
		What: "the books stage's lookup of the book a lesson series explains (pipeline classify/books.py)",
		Off:  `rules books.enabled = false; offline, only the books already looked up are used`},
	{Name: "websearch", Hosts: []string{"www.googleapis.com", "ollama.com"}, By: "auto",
		What: "enrichment's web search (Google or Ollama), which each enrich job makes once an admin set a search key",
		Off:  "no search key; offline, enrichment reads the internal intel only"},
	{Name: "camel-dictionary", Hosts: []string{""}, By: "auto",
		What: "CAMeL Tools' download of its dictionary when its data folder (outside the packs) lacks it",
		Off:  "the model.refine pack; never inside a pack's folder, never offline"},
	{Name: "machine-conversion", Hosts: []string{"github.com"}, By: "admin",
		What: "a machine converted to the Go node fetches the release from the home's mirror, else GitHub",
		Off:  "never started by the server"},
	{Name: "model-downloads", Hosts: []string{"huggingface.co"}, By: "admin",
		What: "a model an admin downloads from the Models page (the pipeline's model service)",
		Off:  "HF_HUB_OFFLINE=1 for the transcription models' cache"},
	{Name: "plugin-store-link", Hosts: []string{"plugins.maktaba.tv"}, By: "admin",
		What: "linking this home to a plugin store account and unlinking it (Epic 54 story 54.15; the home's storelink)",
		Off:  "never started by the server; offline, the store link is refused (store_offline)"},
	{Name: "plugin-store-lists", Hosts: []string{"plugins-cdn.maktaba.tv"}, By: "auto",
		What: "the plugin store's signed lists: the revocation list's head every 5 minutes, the list and the catalog (Epic 54 story 54.17)",
		Off:  "idle while no plugin is installed and Browse was not opened in 7 days; offline, the kept lists age (an expired one warns, never stops a plugin)"},
	{Name: "plugin-store-downloads", Hosts: []string{"plugins-cdn.maktaba.tv"}, By: "auto",
		What: "a plugin package from the store's CDN, for an admin's install from the store or a plugin's nightly automatic update (54.17)",
		Off:  "automatic updates are off per plugin by default; offline, store installs answer store_offline"},
	{Name: "plugin-store-seen", Hosts: []string{"plugins.maktaba.tv"}, By: "auto",
		What: "a linked home's daily list of the store plugins it has installed (54.15 §5)",
		Off:  `[telemetry] enabled = "off", or no store link`},
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
