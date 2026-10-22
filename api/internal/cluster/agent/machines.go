package agent

// Epic 35 (stories 35.8, 35.9): the node supervisor's machine agent on the channel
// (hello.process = "supervisor", worker_id "sup-<node8>"). Right after welcome the hub
// sends it the node's delegation key (`keys`), and again whenever the key rotates
// (daily); every revocation on the home (sign-out, password change, a user disabled or
// removed, an admin's revoke) goes to every machine agent as `auth.revoked` within the
// second, so the nodes drop their cached answers. The agent reports `update.state`
// (its release, platform, layout).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
)

// ProcessSupervisor is hello.process of a node's machine agent.
const ProcessSupervisor = "supervisor"

// KeyIssuer issues and revokes the nodes' delegation keys (delegation.KeyStore).
type KeyIssuer interface {
	Issue(ctx context.Context, nodeID string) (delegation.Key, bool, error)
	Revoke(ctx context.Context, nodeID string)
}

// keysCheckEvery is how often the hub looks for keys to rotate.
var keysCheckEvery = 10 * time.Minute

func (s *Session) machineAgent() bool { return s.Kind == "node" && s.Process == ProcessSupervisor }

func keysFrame(k delegation.Key) map[string]any {
	return map[string]any{"t": "keys", "delegation": map[string]any{
		"id": k.ID, "key": k.Secret, "not_after": k.NotAfter.UTC().Format(time.RFC3339)}}
}

// afterWelcome sends a machine agent its delegation key.
func (h *Hub) afterWelcome(s *Session) {
	if !s.machineAgent() {
		return
	}
	if h.cfg.Delegation != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		k, _, err := h.cfg.Delegation.Issue(ctx, s.NodeID)
		cancel()
		switch {
		case err != nil:
			h.log().Warn("cluster: no delegation key for a machine", "node_id", s.NodeID, "err", err,
				"event", "cluster_delegation_issue_failed")
		case s.send(keysFrame(k)):
			h.event(map[string]any{"event": "keys", "session": s.ID, "node_id": s.NodeID, "key_id": k.ID})
		}
	}
	// what was revoked while it was away (its copy may not have it yet)
	for _, f := range h.recentRevocations() {
		s.send(f)
	}
}

// revokedReplay is how long the home keeps a revocation to send again to a machine agent
// that connects: past the most a copy is allowed to lag (nodeauth's copy reads) and the
// time a machine honours a pushed one.
const revokedReplay = 15 * time.Minute

type revokedFrame struct {
	at    time.Time
	frame map[string]any
}

// keepRevoked keeps a revocation's frame for the agents that connect later.
func (h *Hub) keepRevoked(frame map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.cfg.Now()
	kept := h.recentRevoked[:0]
	for _, f := range h.recentRevoked {
		if now.Sub(f.at) <= revokedReplay {
			kept = append(kept, f)
		}
	}
	h.recentRevoked = append(kept, revokedFrame{at: now, frame: frame})
}

func (h *Hub) recentRevocations() []map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	now := h.cfg.Now()
	var out []map[string]any
	for _, f := range h.recentRevoked {
		if now.Sub(f.at) <= revokedReplay {
			out = append(out, f.frame)
		}
	}
	return out
}

func (h *Hub) machineAgents() []*Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []*Session
	for _, s := range h.sessions {
		if s.machineAgent() {
			out = append(out, s)
		}
	}
	return out
}

// keysLoop rotates the machine agents' keys when a day old.
func (h *Hub) keysLoop(ctx context.Context) {
	if h.cfg.Delegation == nil {
		return
	}
	t := time.NewTicker(keysCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, s := range h.machineAgents() {
			k, rotated, err := h.cfg.Delegation.Issue(ctx, s.NodeID)
			if err == nil && rotated && s.send(keysFrame(k)) {
				h.event(map[string]any{"event": "keys", "session": s.ID, "node_id": s.NodeID, "key_id": k.ID,
					"detail": "rotated"})
			}
		}
	}
}

// RevokedSessions is what a revocation names, as the home's session store reports it.
type RevokedSessions struct {
	SessionIDs []string
	UserID     string
	Reason     string
}

// BroadcastRevoked sends auth.revoked to every machine agent (story 35.9 AC4). Session
// ids travel as their SHA-256 (the nodes key their caches by it). It returns how many
// agents were told.
func (h *Hub) BroadcastRevoked(r RevokedSessions) int {
	if len(r.SessionIDs) == 0 && r.UserID == "" {
		return 0
	}
	hashes := make([]string, 0, len(r.SessionIDs))
	for _, id := range r.SessionIDs {
		sum := sha256.Sum256([]byte(id))
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	frame := map[string]any{"t": "auth.revoked", "reason": r.Reason}
	if len(hashes) > 0 {
		frame["session_sha256"] = hashes
	}
	if r.UserID != "" {
		frame["user_id"] = r.UserID
	}
	h.keepRevoked(frame)
	n := 0
	for _, s := range h.machineAgents() {
		if s.send(frame) {
			n++
		}
	}
	if n > 0 {
		h.event(map[string]any{"event": "auth_revoked", "sessions": len(hashes), "user_id": nullable(r.UserID),
			"agents": n, "detail": r.Reason})
	}
	return n
}

// onUpdateState records a machine agent's update.state (its release and platform).
func (h *Hub) onUpdateState(s *Session, raw []byte) {
	var f struct {
		Version  string `json:"version"`
		Platform string `json:"platform"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return
	}
	s.mu.Lock()
	s.updateState = stripT(raw)
	s.mu.Unlock()
	h.event(map[string]any{"event": "update_state", "session": s.ID, "node_id": nullable(s.NodeID),
		"version": f.Version, "platform": f.Platform})
}
