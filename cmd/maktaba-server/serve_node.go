package main

// Epic 35 phase C in the supervisor (stories 35.8, 35.10, 35.11, design §9, §11.3, §12):
// a machine with a joined node config runs as a full node — the machine agent holds the
// cluster channel and serves the delegation key and the revocations to the node's API
// on the supervisor's loopback admin; the web port sends live TV, the guide and the
// lineup to the home's front door (with the home away, live TV to this machine's own
// channels, from its copy of the home's database: Epic 37), and VOD the home serves
// (the fallback) to the home's streaming port; every machine advertises itself over
// mDNS.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/machineagent"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/mdns"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/webui"
)

// nodeServices is what the supervisor runs for node mode (nil on the home).
type nodeServices struct {
	node  config.NodeConfig
	agent *machineagent.Agent
	token string
	// homeAway: this machine's API says it cannot reach its home (watchHome)
	homeAway atomic.Bool
}

// HomeAway reports whether this machine cannot reach its home now.
func (ns *nodeServices) HomeAway() bool { return ns != nil && ns.homeAway.Load() }

// watchHome follows the API's view of the home (/api/cluster/self, every 5 s): an API
// that does not answer leaves the last view.
func (ns *nodeServices) watchHome(ctx context.Context, api *url.URL) {
	if api == nil {
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for {
		if resp, err := client.Get(api.String() + "/api/cluster/self"); err == nil {
			var s struct {
				Home *struct {
					Reachable bool `json:"reachable"`
				} `json:"home"`
			}
			if json.NewDecoder(resp.Body).Decode(&s) == nil && s.Home != nil {
				if was := ns.homeAway.Swap(!s.Home.Reachable); was != !s.Home.Reachable {
					if was {
						fmt.Fprintln(os.Stderr, "[serve] node: the home answers again: live TV from its front door")
					} else {
						fmt.Fprintln(os.Stderr, "[serve] node: the home is away: live TV from this machine's own channels")
					}
				}
			}
			_ = resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// startNodeServices starts the machine agent when this machine is a full node.
func startNodeServices(ctx context.Context, cfg config.Config) *nodeServices {
	n := supervisor.NodeConfig()
	if !supervisor.NodeMode(cfg, n) {
		return nil
	}
	ns := &nodeServices{node: n}
	tok, err := machineagent.WriteToken(supervisor.LocalTokenPath(cfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[serve] node: cannot write the local token (the API cannot reach the machine agent): %v\n", err)
	}
	ns.token = tok
	host, _ := os.Hostname()
	ns.agent = &machineagent.Agent{
		ID: machineagent.Identity{NodeID: n.NodeID, Node8: n.Node8(), PrivateKey: n.PrivateKey,
			ClusterPub: n.Home.ClusterPub, HomeURL: n.HomeURL(), Host: host},
		State: func() machineagent.UpdateState {
			layout := "flat"
			if os.Getenv("MAKTABA_RELEASE_DIR") != "" {
				layout = "side_by_side"
			}
			return machineagent.UpdateState{Version: version.Version, Platform: runtime.GOOS + "-" + runtime.GOARCH,
				Layout: layout}
		},
	}
	go ns.agent.Run(ctx)
	go ns.watchHome(ctx, apiOrigin(cfg.Server.Listen))
	fmt.Fprintf(os.Stderr, "[serve] full node of %s (node %s): api, web, pipeline, streaming\n", n.HomeURL(), n.Node8())
	return ns
}

// adminHandler adds the machine agent's /agent/* to the supervisor's loopback admin.
func (ns *nodeServices) adminHandler(base http.Handler) http.Handler {
	if ns == nil || ns.agent == nil {
		return base
	}
	mux := http.NewServeMux()
	mux.Handle("/agent/", ns.agent.Handler(ns.token))
	mux.Handle("/", base)
	return mux
}

// homeStreamProxy reverse-proxies to the home's streaming port (the TV front door).
func homeStreamProxy(target string) http.Handler {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			webui.Error(w, r, http.StatusBadGateway, "This machine cannot reach its home",
				"The home's address is not known.")
		})
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = u.Host
		// the web session is for the API only, never a streaming service
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
		r.Header.Del("X-Maktaba-Via")
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		webui.Error(w, r, http.StatusBadGateway, "This machine cannot reach its home",
			"The home's live TV is not reachable ("+err.Error()+").")
	}
	return proxy
}

// viaHomePrefix is a VOD session the home serves (the fallback, design §11.5).
const viaHomePrefix = "/stream/via-home/"

// offlineChannels is this machine's own channels on its streaming port (streaming
// offline_tv.go), for its viewers while the home is away.
const offlineChannels = "/stream/offline"

// nodeStreamRouter sends a node web port's /stream/ by kind (design §11.3): live TV
// (channels) to the home's front door, or with the home away (homeAway) to this
// machine's own channels; a session the home serves (/stream/via-home/…) to the home's
// streaming port; everything else (VOD) to this machine's streaming role, which reads
// the NAS itself.
func nodeStreamRouter(local, home http.Handler, homeAway func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/stream/channel/") && homeAway != nil && homeAway():
			out := r.Clone(r.Context())
			out.URL.Path = offlineChannels + p
			out.URL.RawPath = ""
			w.Header().Set("X-Maktaba-Stream", "local-offline")
			local.ServeHTTP(w, out)
		case strings.HasPrefix(p, "/stream/channel/"):
			w.Header().Set("X-Maktaba-Stream", "home-front-door")
			home.ServeHTTP(w, r)
		case strings.HasPrefix(p, viaHomePrefix):
			out := r.Clone(r.Context())
			out.URL.Path = "/stream/" + strings.TrimPrefix(p, viaHomePrefix)
			out.URL.RawPath = ""
			w.Header().Set("X-Maktaba-Stream", "home-vod-fallback")
			home.ServeHTTP(w, out)
		default:
			w.Header().Set("X-Maktaba-Stream", "local")
			local.ServeHTTP(w, r)
		}
	})
}

// allowedNodeStreamPath is allowedStreamPath plus the fallback's /stream/via-home/.
func allowedNodeStreamPath(p string) bool {
	if strings.HasPrefix(p, viaHomePrefix) {
		return allowedStreamPath("/stream/" + strings.TrimPrefix(p, viaHomePrefix))
	}
	return allowedStreamPath(p)
}

// ---------- mDNS (story 35.11) ----------

type selfInfo struct {
	Role        string `json:"role"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	SchemaRev   int    `json:"schema_rev"`
	AcceptJoins bool   `json:"accept_joins"`
	Home        *struct {
		InstanceID string `json:"instance_id"`
	} `json:"home"`
}

// advertiseMachine is the supervisor's single mDNS publication of this machine. It
// learns what to say from its own API's /api/cluster/self (re-checked every 30 s: a
// renamed home, joins switched on, the home's id once a node reached it) and from the
// node config.
func advertiseMachine(ctx context.Context, cfg config.Config, ns *nodeServices) {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("MAKTABA_DISCOVERY_MDNS")))
	if v == "0" || v == "false" || v == "off" {
		return
	}
	apiPort := mdns.PortFromListen(cfg.Server.Listen, 8080)
	webPort := mdns.PortFromListen(cfg.Server.WebListen, 8088)
	host, _ := os.Hostname()
	m := mdns.Machine{Role: "home", Name: host, WebPort: webPort, Version: version.Version}
	if ns != nil {
		m.Role, m.ID, m.HomeID = "node", ns.node.NodeID, ns.node.Home.InstanceID
		if ns.node.Name != "" {
			m.Name = ns.node.Name
		}
	}
	pub := mdns.NewPublisher()
	defer pub.Close()
	api := apiOrigin(cfg.Server.Listen)
	client := &http.Client{Timeout: 3 * time.Second}
	logged := false
	for {
		if api != nil {
			if resp, err := client.Get(api.String() + "/api/cluster/self"); err == nil {
				var s selfInfo
				if json.NewDecoder(resp.Body).Decode(&s) == nil {
					switch {
					case s.Role == "home" && s.ID != "":
						m.ID, m.HomeID, m.SchemaRev, m.Join = s.ID, s.ID, s.SchemaRev, s.AcceptJoins
						if s.Name != "" {
							m.Name = s.Name
						}
					case s.Role == "node" && s.Home != nil && s.Home.InstanceID != "":
						m.HomeID = s.Home.InstanceID
					}
				}
				_ = resp.Body.Close()
			}
		}
		if changed, err := pub.Publish(m, apiPort); err != nil {
			if !logged {
				fmt.Fprintf(os.Stderr, "[serve] mDNS advertisement failed (continuing): %v\n", err)
				logged = true
			}
		} else if changed {
			fmt.Fprintf(os.Stderr, "[serve] advertising _maktaba._tcp role=%s on port %d\n", m.Role, apiPort)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}
