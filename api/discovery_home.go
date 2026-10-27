package main

// The home's identity for the apps (Epic 42 story 42.2): GET /api/discovery/home and a
// device code's verify_url read it. Its id and name are the instance identity's (the
// mDNS TXT `id` and `name`); its addresses are the home's web address (MAKTABA_WEB_URL,
// set by the supervisor), the same port on this machine's .local name, then the relay
// address when the home is claimed in Maktaba Cloud.

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	discoveryh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/discovery"
	subh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
)

// defaultRelayWS is the relay agent's default tunnel (cloud/cmd/maktaba-cloudlink): a
// claimed home is reached at https://<slug>.<its host>.
const defaultRelayWS = "wss://relay.maktaba.app/v1/relay/ws"

// homeDirectory answers discoveryh.HomeFunc.
type homeDirectory struct {
	// WebURL is the home's web address (MAKTABA_WEB_URL); LocalHost this machine's
	// .local name ("" for none).
	WebURL    string
	LocalHost string
	// Cloud is the home's place in Maktaba Cloud (nil: never claimed); RelayWS the relay
	// agent's tunnel (MAKTABA_RELAY_WS, default defaultRelayWS).
	Cloud   func() subh.CloudLink
	RelayWS string
	// HostName is the name until the instance identity answers (this machine's).
	HostName string

	mu    sync.RWMutex
	store cluster.Store
}

// newHomeDirectory reads the environment the supervisor sets.
func newHomeDirectory(cloud func() subh.CloudLink) *homeDirectory {
	return &homeDirectory{
		WebURL:    os.Getenv("MAKTABA_WEB_URL"),
		LocalHost: discovery.LocalHostName(),
		Cloud:     cloud,
		RelayWS:   os.Getenv("MAKTABA_RELAY_WS"),
		HostName:  cluster.StableHostName(),
	}
}

// setStore gives the directory the instance identity (after the cluster's bootstrap).
func (d *homeDirectory) setStore(s cluster.Store) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.store = s
	d.mu.Unlock()
}

// Info implements discoveryh.HomeFunc.
func (d *homeDirectory) Info(ctx context.Context, _ *http.Request) discoveryh.HomeInfo {
	info := discoveryh.HomeInfo{Version: version.Version, URLs: d.urls()}
	d.mu.RLock()
	store := d.store
	d.mu.RUnlock()
	if store != nil {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if id, err := store.Identity(cctx); err == nil {
			info.ID, info.Name = strings.ToLower(id.InstanceID), id.Name
		}
	}
	if info.Name == "" {
		info.Name = d.HostName
	}
	return info
}

// urls are the home's base URLs: its web address (unless it is loopback), the same on
// this machine's .local name, then the relay address.
func (d *homeDirectory) urls() []string {
	var out []string
	if u, err := url.Parse(strings.TrimRight(strings.TrimSpace(d.WebURL), "/")); err == nil && u.Host != "" {
		if ip := net.ParseIP(u.Hostname()); ip == nil || !ip.IsLoopback() {
			out = append(out, u.String())
		}
		if d.LocalHost != "" && !strings.EqualFold(u.Hostname(), d.LocalHost) {
			local := *u
			local.Host = d.LocalHost
			if p := u.Port(); p != "" {
				local.Host = net.JoinHostPort(d.LocalHost, p)
			}
			out = append(out, local.String())
		}
	}
	if r := d.relayURL(); r != "" {
		out = append(out, r)
	}
	return out
}

// webBasePath is the path of the home's web address ("" for the root): the mDNS TXT
// `path`.
func webBasePath(webURL string) string {
	u, err := url.Parse(strings.TrimSpace(webURL))
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
}

// relayURL is https://<slug>.<relay host> for a claimed home, else "".
func (d *homeDirectory) relayURL() string {
	if d.Cloud == nil {
		return ""
	}
	link := d.Cloud()
	slug := strings.ToLower(strings.TrimSpace(link.Slug))
	if !link.Claimed || slug == "" || strings.ContainsAny(slug, "./:@ ") {
		return ""
	}
	ws := strings.TrimSpace(d.RelayWS)
	if ws == "" {
		ws = defaultRelayWS
	}
	u, err := url.Parse(ws)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return "https://" + slug + "." + strings.ToLower(u.Hostname())
}
