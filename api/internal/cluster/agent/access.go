package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// AccessGranter is the home's side of its machines' database access (the home owns it).
type AccessGranter interface {
	// AccessUsable reports whether a machine's role logs in on this home's database now.
	AccessUsable(ctx context.Context, nodeID string) (bool, error)
	// GrantAccess makes a machine's role again on this home's database, with a new
	// password, and returns its access as the machine's home.access command takes it.
	GrantAccess(ctx context.Context, nodeID string) (json.RawMessage, error)
	// AccessTarget is where a re-made access points: the database's host ("": the host
	// the machine reaches the home at), port and name.
	AccessTarget(ctx context.Context, nodeID string) (host string, port int, db string, err error)
}

// accessRetry is how soon a machine's access is re-made again when it still cannot log
// in; each time after that waits twice as long, up to accessRetryMax (a machine that
// fails for its own reasons, or a database that cannot take a login now, is not rotated
// in a loop).
const (
	accessRetry    = time.Minute
	accessRetryMax = time.Hour
)

// homeDBState is a machine's update.state's home_db.
type homeDBState struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Healing bool   `json:"healing"`
	// Fails is how many logins in a row failed on the machine
	Fails int `json:"fails"`
	// Target is where it logs in: host:port/database
	Target string `json:"target"`
	// HomeHost is the host the machine reaches this home's API at (where an access with no
	// database host points it)
	HomeHost string `json:"home_host"`
}

// remakeWhy is why a machine's re-made access would mend its failed login ("": it would
// not): a refused password or a role the database does not know (auth_failed); any other
// failure only when the machine logs in somewhere else than the access this home hands out
// (the database moved: another port, database or host, whatever the old address does now:
// refuse, close the connection, fail TLS). At the right address, nothing else is the
// access's to mend (TLS, a busy or starting database, an operator's pg_hba).
func remakeWhy(db homeDBState, host string, port int, name string) string {
	if db.Code == "auth_failed" {
		return db.Code
	}
	if moved(db.Target, host, port, name, db.HomeHost) {
		if db.Code == "" {
			return "moved"
		}
		return db.Code + "_moved"
	}
	return ""
}

// moved reports whether a machine's target (host:port/database) is not the access this
// home hands out: its port or database differs, or its host: the one the home names, else
// the one the machine reaches the home's API at (homeHost, when it says).
func moved(target, host string, port int, name, homeHost string) bool {
	addr, db, _ := strings.Cut(target, "/")
	h, p, err := net.SplitHostPort(addr)
	if target == "" || err != nil {
		return false // a machine that does not say where it logs in: not this check's
	}
	n, _ := strconv.Atoi(p)
	want := host
	if want == "" {
		want = homeHost
	}
	return n != port || (name != "" && db != name) || (want != "" && !strings.EqualFold(h, want))
}

// confirmFails is how many failed logins in a row make a machine's failure one the home
// acts on and shows: one try can meet a home in the middle of a switch (the machine still
// on the old home's database, about to follow the new one by itself).
const confirmFails = 2

// off reports whether the machine says, confirmed, that it cannot log in.
func (d homeDBState) off() bool { return !d.OK && d.Fails >= confirmFails }

// homeDBOf is a machine agent's last word on its login to this home's database (ok false
// before it says).
func homeDBOf(s *Session) (homeDBState, bool) {
	s.mu.Lock()
	raw := s.updateState
	s.mu.Unlock()
	var st struct {
		HomeDB *homeDBState `json:"home_db"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &st) != nil || st.HomeDB == nil {
		return homeDBState{}, false
	}
	return *st.HomeDB, true
}

// OffDatabase is a connected machine that says it cannot log in to this home's database.
type OffDatabase struct {
	NodeID  string
	Code    string
	Healing bool
}

// MachinesOffDatabase are the connected machines that say they cannot log in to this
// home's database (their update.state's home_db), with why and whether they follow their
// re-made access already.
func (h *Hub) MachinesOffDatabase() []OffDatabase {
	var out []OffDatabase
	for _, s := range h.machineAgents() {
		if db, said := homeDBOf(s); said && db.off() && s.NodeID != "" {
			out = append(out, OffDatabase{NodeID: s.NodeID, Code: db.Code, Healing: db.Healing})
		}
	}
	return out
}

// EnsureAccess keeps every connected machine able to log in to this home's database: a
// machine that says it cannot, twice in a row, for a reason a re-made access mends
// (remakeWhy: its role refused, or the database moved to another port or database), or
// whose role does not log in on the home's current database (checked once per agent
// session: a move carried no roles) gets its access re-made and pushed (home.access); again
// after accessRetry, then twice as long each time up to accessRetryMax, until it logs in.
// It runs every interval until ctx ends.
func (h *Hub) EnsureAccess(ctx context.Context, g AccessGranter, every time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	// a machine's own request (access.request) is answered with g too
	h.AnswerAccessRequests(g, logger)
	granted := map[string]time.Time{} // node -> when its access was last re-made
	tries := map[string]int{}         // node -> re-makes since it last logged in
	checked := map[string]bool{}      // agent sessions whose role was checked
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		live := map[string]bool{}
		for _, s := range h.machineAgents() {
			if s.NodeID == "" {
				continue
			}
			live[s.ID] = true
			why := ""
			db, said := homeDBOf(s)
			if said && db.OK {
				delete(tries, s.NodeID) // it logs in: a later failure starts the backoff afresh
			}
			if said && db.off() && !db.Healing {
				if host, port, name, err := g.AccessTarget(ctx, s.NodeID); err == nil {
					why = remakeWhy(db, host, port, name)
				}
			} else if !checked[s.ID] {
				usable, err := g.AccessUsable(ctx, s.NodeID)
				if err == nil {
					checked[s.ID] = true
					if !usable {
						why = "role_unusable"
					}
				}
			}
			if why == "" || (!granted[s.NodeID].IsZero() && h.now().Sub(granted[s.NodeID]) < retryAfter(tries[s.NodeID])) {
				continue
			}
			if t := h.requestGranted(s.NodeID); !t.IsZero() && h.now().Sub(t) < accessRetry {
				continue // its own request re-made it a moment ago: it follows that one
			}
			granted[s.NodeID] = h.now()
			tries[s.NodeID]++
			args, err := g.GrantAccess(ctx, s.NodeID)
			if err != nil {
				logger.Warn("cluster: a machine's database access cannot be made again", "node_id", s.NodeID, "why", why,
					"err", err, "event", "cluster_access_failed")
				continue
			}
			res, err := h.CommandWithin(ctx, s.ID, "home.access", args, "system", 30*time.Second)
			if err != nil || !res.OK {
				detail := ""
				if err != nil {
					detail = err.Error()
				} else {
					detail = string(res.Detail)
				}
				logger.Warn("cluster: a machine did not take its re-made database access", "node_id", s.NodeID,
					"why", why, "detail", detail, "event", "cluster_access_not_taken")
				continue
			}
			logger.Info("cluster: a machine could not log in to this home's database: its access was made again",
				"node_id", s.NodeID, "why", why, "event", "cluster_access_remade")
		}
		for sid := range checked {
			if !live[sid] {
				delete(checked, sid)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// retryAfter is how long after its last re-made access a machine waits for the next:
// accessRetry after the first, twice as long after each one more, up to accessRetryMax.
func retryAfter(tries int) time.Duration {
	d := accessRetry
	for i := 1; i < tries && d < accessRetryMax; i++ {
		d *= 2
	}
	return min(d, accessRetryMax)
}

// accessAskEvery is how soon a machine may ask for its database access again
// (access.request): a machine asks a minute after its first request at the soonest (its
// own backoff), so a request sooner is refused, never a password changed in a loop.
const accessAskEvery = 30 * time.Second

// accessRequests are the machines' own requests for their database access.
type accessRequests struct {
	mu      sync.Mutex
	granter AccessGranter
	logger  *slog.Logger
	asked   map[string]time.Time // node -> its last request
	granted map[string]time.Time // node -> when a request of its re-made its access
}

// AnswerAccessRequests answers the machines' own requests for their database access with g
// (EnsureAccess does, besides its push).
func (h *Hub) AnswerAccessRequests(g AccessGranter, logger *slog.Logger) {
	h.acc.mu.Lock()
	h.acc.granter, h.acc.logger = g, logger
	h.acc.mu.Unlock()
}

// requestGranted is when a machine's own request last re-made its access (zero: never).
func (h *Hub) requestGranted(nodeID string) time.Time {
	h.acc.mu.Lock()
	defer h.acc.mu.Unlock()
	return h.acc.granted[nodeID]
}

// onAccessRequest answers a machine agent's access.request ({id, home_db}): a machine that
// cannot log in to this home's database, and got no re-made access from the home's own
// push, asks for one. The access is made for the session's own machine (the handshake
// proved it: never a machine a frame names), as the push makes it (GrantAccess, then
// home.access), at most once per accessAskEvery per machine, and the machine gets an
// access.answer {id, ok, code, reason}: what stops the home, with its code
// (cluster.AccessError), or not_taken with the machine's own answer. Every request is in
// the events file.
//
// The push (EnsureAccess) and this pull can both re-make the same machine's access within
// moments (the push's retry and the machine's request crossing): each GrantAccess sets a
// new password, so the first home.access the machine takes is outdated by the second.
// That heals by itself: the machine keeps the last home.access it took, which is the
// role's current password, and a login refused in between is retried at its next check.
func (h *Hub) onAccessRequest(s *Session, raw []byte) {
	var f struct {
		ID string `json:"id"`
		// HomeDB is the machine's failed login it asks for (its update.state's when the
		// frame has none)
		HomeDB *homeDBState `json:"home_db"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return
	}
	if f.HomeDB == nil {
		if db, said := homeDBOf(s); said {
			f.HomeDB = &db
		}
	}
	answer := func(ok bool, code, reason string) {
		s.send(map[string]any{"t": "access.answer", "id": f.ID, "ok": ok, "code": code, "reason": reason})
	}
	if !s.machineAgent() || s.NodeID == "" {
		h.accessAnswered(s, h.log(), false, cluster.AccessUnknown, "only a machine's agent asks for its database access", answer)
		return
	}
	now := h.now()
	h.acc.mu.Lock()
	g, logger := h.acc.granter, h.acc.logger
	last := h.acc.asked[s.NodeID]
	limited := !last.IsZero() && now.Sub(last) < accessAskEvery
	if g != nil && !limited {
		if h.acc.asked == nil {
			h.acc.asked = map[string]time.Time{}
		}
		h.acc.asked[s.NodeID] = now
	}
	h.acc.mu.Unlock()
	if logger == nil {
		logger = h.log()
	}
	switch {
	case g == nil:
		h.accessAnswered(s, logger, false, cluster.AccessOther,
			"this home gives no database access now (it is starting, or its database is not Postgres)", answer)
	case limited:
		h.accessAnswered(s, logger, false, cluster.AccessRateLimited, fmt.Sprintf("asked again too soon: "+
			"ask again in %s", (accessAskEvery-now.Sub(last)).Round(time.Second)), answer)
	default:
		go h.grantRequested(s, g, f.HomeDB, logger, answer)
	}
}

// requestMends is why a machine's own request (its failed login db) is met with a re-made
// access ("": it is not): as the push re-makes one (remakeWhy: a refused password, a
// database that moved), or a role that does not log in on this home's database
// (role_unusable). A TLS failure at the right address is neither: the home requiring the
// machine's certificate, the machine not verifying the home's. A new password mends none
// of it, and made anyway it was one the machine's roles then no longer had (they log in
// as before, the failed check alone took it): every request one more.
func requestMends(ctx context.Context, g AccessGranter, nodeID string, db homeDBState) string {
	if host, port, name, err := g.AccessTarget(ctx, nodeID); err == nil {
		if why := remakeWhy(db, host, port, name); why != "" {
			return why
		}
	} else if !notMendable(db.Code) {
		return "requested" // where it points is not known here: made again, as asked
	}
	if usable, err := g.AccessUsable(ctx, nodeID); err == nil && !usable {
		return "role_unusable"
	}
	return ""
}

// notMendable reports a machine's failed login no re-made access mends wherever the
// database is: TLS (its certificate, the home's), or a certificate for another role than
// its login's (cert_mismatch: a re-made access keeps the same role).
func notMendable(code string) bool { return code == "tls" || code == "cert_mismatch" }

// grantRequested re-makes a machine's access at its own request (db: the failed login it
// asks for, nil when it does not say) and sends it (home.access), when a re-made access
// mends it (requestMends).
func (h *Hub) grantRequested(s *Session, g AccessGranter, db *homeDBState, logger *slog.Logger,
	answer func(ok bool, code, reason string)) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if db != nil && requestMends(ctx, g, s.NodeID, *db) == "" {
		h.accessAnswered(s, logger, false, cluster.AccessNotMendable, fmt.Sprintf("a new login would not mend "+
			"this machine's (%s at the right address): its roles keep the one they have", db.Code), answer)
		return
	}
	args, err := g.GrantAccess(ctx, s.NodeID)
	if err != nil {
		h.accessAnswered(s, logger, false, cluster.AccessCode(err), err.Error(), answer)
		return
	}
	h.acc.mu.Lock()
	if h.acc.granted == nil {
		h.acc.granted = map[string]time.Time{}
	}
	h.acc.granted[s.NodeID] = h.now()
	h.acc.mu.Unlock()
	res, err := h.CommandWithin(ctx, s.ID, "home.access", args, "system", 30*time.Second)
	switch {
	case err != nil:
		h.accessAnswered(s, logger, false, cluster.AccessNotTaken, "the access did not reach the machine: "+err.Error(), answer)
	case !res.OK:
		h.accessAnswered(s, logger, false, cluster.AccessNotTaken, "the machine did not take it: "+detailText(res.Detail), answer)
	default:
		h.accessAnswered(s, logger, true, "", "its access was made again and sent to it", answer)
	}
}

// accessAnswered records a machine's request and its answer (the events file, the log),
// then answers it.
func (h *Hub) accessAnswered(s *Session, logger *slog.Logger, ok bool, code, reason string,
	answer func(ok bool, code, reason string)) {
	h.event(map[string]any{"event": "access_request", "session": s.ID, "node_id": nullable(s.NodeID), "ok": ok,
		"code": nullable(code), "detail": reason})
	if ok {
		logger.Info("cluster: a machine asked for its database access: made again and sent", "node_id", s.NodeID,
			"event", "cluster_access_requested")
	} else {
		logger.Warn("cluster: a machine asked for its database access, which this home cannot give", "node_id", s.NodeID,
			"code", code, "reason", reason, "event", "cluster_access_request_refused")
	}
	answer(ok, code, reason)
}

// detailText is a command result's detail as text (a JSON string unquoted).
func detailText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
