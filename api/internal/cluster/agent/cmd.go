package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Commands a process accepts (design-agent.md §3).
var commandNames = map[string]bool{
	"drain": true, "undrain": true, "reload_stages": true, "log_level": true, "disconnect": true,
}

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
	s := h.Session(sessionID)
	if s == nil {
		return CmdResult{}, ErrNoSession
	}
	if !commandNames[name] {
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
	h.event(map[string]any{"event": "cmd", "session": s.ID, "ip": s.IP, "worker_id": s.WorkerID, "id": id,
		"name": name, "args": argMap, "by": by})
	if !s.send(map[string]any{"t": "cmd", "id": id, "name": name, "args": argMap}) {
		s.mu.Lock()
		delete(s.cmds, id)
		s.mu.Unlock()
		return CmdResult{}, ErrNoSession
	}
	t := time.NewTimer(CmdTimeout)
	defer t.Stop()
	var res CmdResult
	var err error
	select {
	case res = <-ch:
	case <-t.C:
		err = ErrCmdTimeout
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		s.mu.Lock()
		delete(s.cmds, id)
		s.mu.Unlock()
	}
	if name == "disconnect" {
		s.closeWith(statusNormal, "disconnected by an admin")
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
	h.event(map[string]any{"event": "cmd.result", "session": s.ID, "ip": s.IP, "id": id, "name": name, "ok": res.OK,
		"detail": res.Detail})
	return res, nil
}
