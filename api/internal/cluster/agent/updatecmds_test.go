package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// An update's commands reach a machine's own agent (Epic 35 story 35.12 AC2) and its
// update.state is the driver's to read; any other session is refused them.
func TestUpdateCommandsReachOnlyAMachineAgent(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	a.send(map[string]any{"t": "update.state", "version": "1.4.0", "platform": "linux-amd64", "layout": "runtime",
		"run": map[string]any{"run_id": "r1", "state": "prepared"}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, ok := f.hub.MachineStates()[id]; ok {
			var s struct {
				Version string `json:"version"`
				Layout  string `json:"layout"`
			}
			if json.Unmarshal(st, &s) != nil || s.Version != "1.4.0" || s.Layout != "runtime" {
				t.Fatalf("machine state %s", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no machine state")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, name := range []string{"update.prepare", "update.apply", "update.status", "update.abort", "update.rollback"} {
		done := make(chan error, 1)
		var res CmdResult
		go func() {
			var err error
			res, err = f.hub.CommandWithin(context.Background(), a.Session, name, json.RawMessage(`{"run_id":"r1"}`),
				"updates", 5*time.Second)
			done <- err
		}()
		cmd := a.recvType("cmd")
		if cmd["name"] != name {
			t.Fatalf("%s: the machine got %v", name, cmd)
		}
		a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true, "detail": map[string]any{"status": nil}})
		if err := <-done; err != nil || !res.OK {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
	}

	w, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "pipeline"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.hub.CommandWithin(context.Background(), w.Session, "update.apply", nil, "updates", time.Second); err != ErrBadCommand {
		t.Fatalf("update.apply to a pipeline session: %v", err)
	}
}
