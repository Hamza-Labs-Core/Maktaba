package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Commands a process accepts (design-agent.md §3), and the ones a machine agent takes
// for a switch of the home (specs/epics/37-node-replica: its copy's marker, the kit's
// chunks, the promotion, whether it promoted, the new home's address).
var commandNames = map[string]bool{
	"drain": true, "undrain": true, "reload_stages": true, "log_level": true, "disconnect": true,
	"switch.prepare": true, "switch.kit": true, "switch.promote": true, "switch.status": true,
	"home.moved": true, "home.database": true,
	// a machine's database access, made again on the home's current database (the home
	// owns it): machine agents only, its password never logged
	"home.access": true,
	// a standby home's kit, and where the standbys are (Epic 37, "Emergency promotion")
	"standby.kit": true, "standby.commit": true, "standby.forget": true, "home.standbys": true,
	// a machine's part of an automatic update (Epic 35 story 35.12): machine agents only
	"update.prepare": true, "update.apply": true, "update.status": true, "update.abort": true, "update.rollback": true,
	// a worker-only machine made a full node by its next update (story 35.8 AC8): its
	// pipeline worker's agent (it has no machine agent yet), never a machine agent
	"update.convert": true,
	// a machine's own log files over a window, for the logs page and the bundle
	"logs.files": true,
}

// adminCommands are the commands an admin may send a process from the processes page
// (POST /api/cluster/processes/{session}/cmd); every other is the home's own to send.
var adminCommands = map[string]bool{"drain": true, "undrain": true, "reload_stages": true, "log_level": true,
	"disconnect": true}

// machineOnly are the commands only a machine's own agent (its supervisor) takes.
func machineOnly(name string) bool {
	return (strings.HasPrefix(name, "update.") && name != "update.convert") || name == "home.access" || name == "logs.files"
}

// workerOnly are the commands only a machine's pipeline worker takes (not its machine
// agent, not a home process).
func workerOnly(name string) bool { return name == "update.convert" }

// bulkyResults are the commands whose answers the event log notes by size only (a
// machine's log lines are not the hub's events).
var bulkyResults = map[string]bool{"logs.files": true}

// secretArgs are the commands' arguments the event log never shows.
var secretArgs = map[string][]string{"home.access": {"password"}, "update.convert": {"download_token"}}

// Accepts reports whether the hub sends a command of this name at all (CommandWithin
// refuses any other with ErrBadCommand): the switch's tests ask it of their fake hub.
func Accepts(name string) bool { return commandNames[name] }

// sealedChunks are the commands that carry a sealed kit's chunk: its bytes stay out of the
// event log (their size is noted instead).
var sealedChunks = map[string]bool{"switch.kit": true, "standby.kit": true}

// Command errors.
var (
	ErrNoSession  = errors.New("agent: no such session")
	ErrBadCommand = errors.New("agent: unknown command or bad arguments")
	ErrCmdTimeout = errors.New("agent: the process did not answer in time")
)

// CmdResult is a process's answer (cmd.result).
type CmdResult struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Detail json.RawMessage `json:"detail"`
}

func (s *Session) deliverCmd(r CmdResult) {
	s.mu.Lock()
	ch := s.cmds[r.ID]
	delete(s.cmds, r.ID)
	s.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

func (s *Session) failPendingCmds() {
	s.mu.Lock()
	cmds := s.cmds
	s.cmds = map[string]chan CmdResult{}
	s.mu.Unlock()
	for id, ch := range cmds {
		ch <- CmdResult{ID: id, OK: false, Detail: json.RawMessage(`"disconnected"`)}
	}
}

// Command sends a command to a process and waits for its cmd.result (at
// most CmdTimeout). "disconnect" closes the session once answered (or at
// the timeout). by names who asked (for the events file).
func (h *Hub) Command(ctx context.Context, sessionID, name string, args json.RawMessage, by string) (CmdResult, error) {
	return h.CommandWithin(ctx, sessionID, name, args, by, CmdTimeout)
}

// CommandWithin is Command waiting up to wait (a promotion takes minutes).
func (h *Hub) CommandWithin(ctx context.Context, sessionID, name string, args json.RawMessage, by string, wait time.Duration) (CmdResult, error) {
	s := h.Session(sessionID)
	if s == nil {
		return CmdResult{}, ErrNoSession
	}
	if !commandNames[name] || (machineOnly(name) && !s.machineAgent()) || (workerOnly(name) && !s.nodeWorker()) {
		return CmdResult{}, ErrBadCommand
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	var argMap map[string]any
	if json.Unmarshal(args, &argMap) != nil {
		return CmdResult{}, ErrBadCommand
	}
	if name == "log_level" {
		lv, _ := argMap["level"].(string)
		if normLevel(lv) == "" {
			return CmdResult{}, ErrBadCommand
		}
		argMap["level"] = normLevel(lv)
	}
	id := newID()
	ch := make(chan CmdResult, 1)
	s.mu.Lock()
	s.cmds[id] = ch
	s.mu.Unlock()
	logged := argMap
	if keys := secretArgs[name]; len(keys) > 0 {
		logged = map[string]any{}
		for k, v := range argMap {
			logged[k] = v
		}
		for _, k := range keys {
			if _, set := logged[k]; set {
				logged[k] = "(set, not logged)"
			}
		}
	}
	if sealedChunks[name] {
		logged = map[string]any{}
		for k, v := range argMap {
			logged[k] = v
		}
		if d, ok := logged["data"].(string); ok {
			logged["data"] = fmt.Sprintf("(%d bytes, sealed)", len(d))
		}
	}
	h.event(map[string]any{"event": "cmd", "session": s.ID, "ip": s.IP, "worker_id": s.WorkerID, "id": id,
		"name": name, "args": logged, "by": by})
	if !s.send(map[string]any{"t": "cmd", "id": id, "name": name, "args": argMap}) {
		s.mu.Lock()
		delete(s.cmds, id)
		s.mu.Unlock()
		return CmdResult{}, ErrNoSession
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	var res CmdResult
	var err error
	select {
	case res = <-ch:
	case <-t.C:
		err = ErrCmdTimeout
	case <-ctx.Done():
		err = ctx.Err()
	case <-s.ctx.Done():
		// a session that closes (replaced by its reconnect, dropped) answers nothing more:
		// waiting out the timeout would hold the caller back from the session that replaced it
		select {
		case res = <-ch: // its answer came in just before it closed
		default:
			err = ErrNoSession
		}
	}
	if err != nil {
		s.mu.Lock()
		delete(s.cmds, id)
		s.mu.Unlock()
	}
	if name == "disconnect" {
		s.closeWith(CloseDisconnect, "by an admin")
		if err == nil && !res.OK && string(res.Detail) == `"disconnected"` {
			res.OK = true
		}
		if err != nil {
			res, err = CmdResult{ID: id, OK: true, Detail: json.RawMessage(`"disconnected"`)}, nil
		}
	}
	if err != nil {
		h.event(map[string]any{"event": "cmd.result", "session": s.ID, "ip": s.IP, "id": id, "name": name, "ok": false,
			"detail": err.Error()})
		return CmdResult{ID: id}, err
	}
	res.ID = id
	if res.Detail == nil {
		res.Detail = json.RawMessage("null")
	}
	if name == "log_level" && res.OK {
		s.mu.Lock()
		s.logLevel, _ = argMap["level"].(string)
		s.mu.Unlock()
	}
	var logDetail any = res.Detail
	if bulkyResults[name] {
		logDetail = fmt.Sprintf("(%d bytes)", len(res.Detail))
	}
	h.event(map[string]any{"event": "cmd.result", "session": s.ID, "ip": s.IP, "id": id, "name": name, "ok": res.OK,
		"detail": logDetail})
	return res, nil
}

// MachineAgent is the session of a machine's own agent (its supervisor), "" when it is
// not connected.
func (h *Hub) MachineAgent(nodeID string) string {
	for _, s := range h.nodeSessions(nodeID) {
		if s.Process == "supervisor" {
			return s.ID
		}
	}
	return ""
}

// MachineAgents are the connected machines' own agents (their supervisors): session
// id by node id. A machine's agent that reconnects is registered before the session it
// replaces is closed, and that one stays listed until its connection has shut, which can
// take seconds. So a closed session is never named, and of two live ones the newer is:
// naming the old one would leave the new one untold until the old one is removed.
func (h *Hub) MachineAgents() map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	picked := map[string]*Session{}
	for _, s := range h.sessions {
		if s.Process != "supervisor" || s.NodeID == "" || s.Kind == "home" || s.ctx.Err() != nil {
			continue
		}
		if cur, ok := picked[s.NodeID]; ok && !newer(s, cur) {
			continue
		}
		picked[s.NodeID] = s
	}
	out := make(map[string]string, len(picked))
	for node, s := range picked {
		out[node] = s.ID
	}
	return out
}

// newer reports whether a connected after b (the session id breaks a tie).
func newer(a, b *Session) bool {
	if !a.ConnectedAt.Equal(b.ConnectedAt) {
		return a.ConnectedAt.After(b.ConnectedAt)
	}
	return a.ID > b.ID
}

// NodeWorker is a machine's pipeline worker on the channel: its session, its worker id
// and the pipeline version it reported.
type NodeWorker struct {
	Session, WorkerID, Version string
}

// NodeWorkers are the connected machines' pipeline workers, one by node id (the lowest
// worker id of the machine's: maktaba-node before maktaba-node@2): a worker-only
// machine's next update reaches it there (update.convert).
func (h *Hub) NodeWorkers() map[string]NodeWorker {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := map[string]NodeWorker{}
	for id, s := range h.sessions {
		if !s.nodeWorker() || s.NodeID == "" {
			continue
		}
		if cur, ok := out[s.NodeID]; ok && cur.WorkerID <= s.WorkerID {
			continue
		}
		out[s.NodeID] = NodeWorker{Session: id, WorkerID: s.WorkerID, Version: s.Version}
	}
	return out
}

// NodeHosts are the host names each connected machine's sessions report (its machine
// agent and its workers), by node id: a job's claimer ("host/pid/id") is one of them,
// whatever the machine is named in the cluster.
func (h *Hub) NodeHosts() map[string][]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := map[string][]string{}
	for _, s := range h.sessions {
		if s.NodeID == "" || s.Kind == "home" || s.Host == "" {
			continue
		}
		seen := false
		for _, x := range out[s.NodeID] {
			seen = seen || x == s.Host
		}
		if !seen {
			out[s.NodeID] = append(out[s.NodeID], s.Host)
		}
	}
	return out
}

// HomeProcesses are the sessions of the home's own processes (its workers).
func (h *Hub) HomeProcesses() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []string
	for id, s := range h.sessions {
		if s.Kind == "home" {
			out = append(out, id)
		}
	}
	return out
}
