package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// AccessGranter is the home's side of its machines' database access (the home owns it).
type AccessGranter interface {
	// AccessUsable reports whether a machine's role logs in on this home's database now.
	AccessUsable(ctx context.Context, nodeID string) (bool, error)
	// GrantAccess makes a machine's role again on this home's database, with a new
	// password, and returns its access as the machine's home.access command takes it.
	GrantAccess(ctx context.Context, nodeID string) (json.RawMessage, error)
}

// accessRetry is how soon a machine's access is re-made again when it still cannot log in.
const accessRetry = time.Minute

// homeDBState is a machine's update.state's home_db.
type homeDBState struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Healing bool   `json:"healing"`
	// Fails is how many logins in a row failed on the machine
	Fails int `json:"fails"`
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
// machine that says it cannot (its update.state's home_db: its role refused, the
// database moved to another cluster or port) or whose role does not log in on the home's
// current database (checked once per agent session: a move carried no roles) gets its
// access re-made and pushed (home.access), at most once per accessRetry each. It runs
// every interval until ctx ends.
func (h *Hub) EnsureAccess(ctx context.Context, g AccessGranter, every time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	granted := map[string]time.Time{} // node -> when its access was last re-made
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
			if db, said := homeDBOf(s); said && db.off() && !db.Healing {
				why = db.Code
			} else if !checked[s.ID] {
				usable, err := g.AccessUsable(ctx, s.NodeID)
				if err == nil {
					checked[s.ID] = true
					if !usable {
						why = "role_unusable"
					}
				}
			}
			if why == "" || (!granted[s.NodeID].IsZero() && h.now().Sub(granted[s.NodeID]) < accessRetry) {
				continue
			}
			granted[s.NodeID] = h.now()
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
