package agent

import (
	"testing"
)

// A revocation reaches every machine agent connected, and one that connects within
// revokedReplay of it (it was away: its copy may still say the session is live).
func TestARevocationReachesAMachineThatWasAway(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio")
	here, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	if n := f.hub.BroadcastRevoked(RevokedSessions{SessionIDs: []string{"s1"}, UserID: "u1", Reason: "sign_out"}); n != 1 {
		t.Fatalf("told %d agents", n)
	}
	if m := here.recvType("auth.revoked"); m["user_id"] != "u1" {
		t.Fatalf("the revocation: %v", m)
	}
	otherID, otherPriv := f.approvedNode("den")
	later, err := f.dialAgent(helloOpts{kind: "node", nodeID: otherID, nodePriv: otherPriv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	m := later.recvType("auth.revoked")
	hashes, _ := m["session_sha256"].([]any)
	if m["user_id"] != "u1" || len(hashes) != 1 {
		t.Fatalf("the revocation sent again: %v", m)
	}
}
