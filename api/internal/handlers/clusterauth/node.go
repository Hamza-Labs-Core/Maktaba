package clusterauth

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// SelfInfo is GET /api/cluster/self (local on every machine, public): this machine,
// its home, and whether it can serve now. Never a library or user name.
type SelfInfo struct {
	Role      string `json:"role"` // home | node
	ID        string `json:"id"`   // instance id (home) or node id (node)
	Name      string `json:"name,omitempty"`
	Version   string `json:"version"`
	SchemaRev int    `json:"schema_rev"`
	// Term is the cluster's term on the home (Epic 37: each switch of the home begins
	// the next).
	Term      int64     `json:"term,omitempty"`
	WebURL    string    `json:"web_url,omitempty"`
	Home      *SelfHome `json:"home,omitempty"`
	Paused    string    `json:"paused,omitempty"`
	AgentLink *bool     `json:"agent_connected,omitempty"`
	VOD       any       `json:"vod,omitempty"`
	// Copy is a full node's copy of its home's database (Epic 37): its supervisor's
	// status of it (state, lag, when it last applied the home's changes).
	Copy     json.RawMessage `json:"copy,omitempty"`
	ServedAt time.Time       `json:"served_at"`
	Machines []SelfRow       `json:"machines,omitempty"`
	// AcceptJoins: the home's "accept new machines" switch (its mDNS join=1).
	AcceptJoins bool `json:"accept_joins,omitempty"`
	// MachinesDB are the home's machines that say they cannot log in to its database (the
	// home re-makes their access): an update's watch waits for them to follow it.
	MachinesDB []SelfMachineDB `json:"machines_db,omitempty"`
	// UpdatesAuto is whether the home installs releases by itself (rules.updates
	// auto_install as this build applies it; absent on a node): the Linux app leaves a
	// newer payload to the home's run when it does. Said to a program on this machine (the
	// app asks over loopback) or its supervisor only, never to the LAN.
	UpdatesAuto *bool `json:"updates_auto,omitempty"`
	// Update is an update in flight here: on the home, the cluster's update run; on a node,
	// this machine's own part (its supervisor's journal). Absent when none is. The paused
	// page says what is happening with it.
	Update *SelfUpdate `json:"update,omitempty"`
}

// SelfUpdate is an update in flight: its release, where it is (the run's state on the
// home, the machine's journal state on a node) and since when. Never a person's name.
type SelfUpdate struct {
	Version string     `json:"version"`
	State   string     `json:"state"`
	Step    string     `json:"step,omitempty"`
	Since   *time.Time `json:"since,omitempty"`
}

// SelfMachineDB is a machine that cannot log in to its home's database.
type SelfMachineDB struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Code    string `json:"code,omitempty"`
	Healing bool   `json:"healing,omitempty"`
}

// SelfHome is a node's view of its home.
type SelfHome struct {
	InstanceID   string     `json:"instance_id,omitempty"`
	Name         string     `json:"name,omitempty"`
	WebURL       string     `json:"web_url,omitempty"`
	Version      string     `json:"version,omitempty"`
	Reachable    bool       `json:"reachable"`
	FailingSince *time.Time `json:"failing_since,omitempty"`
	// Update is the home's update run in flight, as its /api/cluster/self says it.
	Update *SelfUpdate `json:"update,omitempty"`
}

// SelfRow is one machine's address (the picker learns the cluster's other addresses
// from a machine it reached, story 35.11 EC2).
type SelfRow struct {
	Role   string `json:"role"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	WebURL string `json:"web_url,omitempty"`
}

// SelfFunc builds this machine's SelfInfo.
type SelfFunc func(ctx context.Context) SelfInfo

// LocalTokenHeader carries the supervisor's per-boot local token
// (MAKTABA_SUPERVISOR_TOKEN_FILE): the home's own update watch reads which machines cannot
// log in to its database from /api/cluster/self with it.
const LocalTokenHeader = mw.LocalTokenHeader

// trusted reports whether a request may see the home's machines: this machine's own
// supervisor, with its local token (/api/cluster/self is public, and carries no session:
// the pages read the machines' database logins from /api/cluster and /api/cluster/updates).
func trusted(r *http.Request) bool { return mw.FromSupervisor(r) }

// MountSelf mounts GET /api/cluster/self alone (the home serves it too). Its machines
// that cannot log in to the home's database (names and why) go to this machine's
// supervisor only; whether it installs releases by itself, to a program on this machine
// too (middleware.LocalCaller: the router's chain marks it).
func MountSelf(r chi.Router, self SelfFunc) {
	r.Get("/api/cluster/self", func(w http.ResponseWriter, r *http.Request) {
		info := SelfInfo{Role: "home"}
		if self != nil {
			info = self(r.Context())
		}
		sup := trusted(r)
		if !sup {
			info.MachinesDB = nil
		}
		if !sup && !mw.LocalCaller(r) {
			info.UpdatesAuto = nil
		}
		info.ServedAt = time.Now().UTC()
		w.Header().Set("Cache-Control", "no-store")
		common.WriteJSON(w, r, http.StatusOK, info)
	})
}

// RemovedRoutes are the routes of the cluster sign-in that was here (Epic 35 story 35.9:
// "Continue with <home>" on a node's sign-in page, a one-time code from the home), removed:
// the sign-in form is the only way in on every machine (a node checks it against the
// home's accounts). Every machine still answers them, 404 to anyone (they pass the
// sign-in gate: middleware.DefaultPublicAllowlist; a node serves them itself:
// router.Placement), so a page or a machine of an older release is told they are gone,
// not asked to sign in.
var RemovedRoutes = []string{"/api/cluster/sso/start", "/api/cluster/sso/callback",
	"/api/cluster/sso/authorize", "/api/cluster/sso/exchange"}

// MountRemoved answers RemovedRoutes 404, whatever the method.
func MountRemoved(mux chi.Router) {
	for _, p := range RemovedRoutes {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			httperror.Write(w, r, httperror.NotFound("cluster sign-in was removed: sign in on this machine"))
		})
	}
}

// HomeInfoCache keeps the home's /api/cluster/self for a node (30 s). Only the first
// read waits for the home (3 s at most); past it a stale answer is given at once and
// read again in the background, so a home that does not answer never holds up the
// machine's own /api/cluster/self.
type HomeInfoCache struct {
	Link homelink.Link
	TTL  time.Duration

	mu      sync.Mutex
	at      time.Time
	info    SelfInfo
	ok      bool
	reading bool
}

func (c *HomeInfoCache) read(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	raw, err := c.Link.Self(ctx)
	cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reading = false
	if err != nil {
		return
	}
	var s SelfInfo
	if json.Unmarshal(raw, &s) == nil {
		c.info, c.ok, c.at = s, true, time.Now()
	}
}

// Get returns the home's self (cached; stale on failure).
func (c *HomeInfoCache) Get(ctx context.Context) (SelfInfo, bool) {
	ttl := c.TTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	c.mu.Lock()
	switch {
	case c.ok && time.Since(c.at) < ttl, c.reading:
	case c.ok:
		c.reading = true
		rctx := context.WithoutCancel(ctx)
		safego.Go(nil, "clusterauth_node_read", func() { c.read(rctx) })
	default:
		c.reading = true
		c.mu.Unlock()
		c.read(ctx)
		c.mu.Lock()
	}
	defer c.mu.Unlock()
	return c.info, c.ok
}
