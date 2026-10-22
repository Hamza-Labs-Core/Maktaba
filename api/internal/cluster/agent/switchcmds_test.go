package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Every command a switch of the home sends a machine's agent goes through the real hub
// and back (the switch's own tests use a fake hub): one the hub did not know would never
// leave it, and the old home would never hear, say, that its switch was called off.
func TestTheSwitchsCommandsReachAMachine(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"switch.prepare", "switch.kit", "switch.promote", "switch.status",
		"home.moved", "home.database", "home.standbys", "standby.kit", "standby.commit", "standby.forget"} {
		done := make(chan error, 1)
		var res CmdResult
		go func() {
			var err error
			res, err = f.hub.CommandWithin(context.Background(), a.Session, name,
				json.RawMessage(`{"id":"sw-1","term":2}`), "switchover", 5*time.Second)
			done <- err
		}()
		cmd := a.recvType("cmd")
		if cmd["name"] != name {
			t.Fatalf("%s: the machine got %v", name, cmd)
		}
		a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true, "detail": "called_off"})
		if err := <-done; err != nil || !res.OK || string(res.Detail) != `"called_off"` {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
	}
	if _, err := f.hub.CommandWithin(context.Background(), a.Session, "switch.nothing", nil, "switchover", time.Second); err != ErrBadCommand {
		t.Fatalf("an unknown command: %v", err)
	}
}
