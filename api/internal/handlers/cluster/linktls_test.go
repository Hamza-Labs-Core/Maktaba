package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// A join that carries a certificate request gets its cluster-link certificate sealed in
// the approval (story 35.16 AC2); one without (an older machine) gets none and asks on
// its channel session.
func TestJoinCSRSealsTheLinkCertificate(t *testing.T) {
	f := newFixture(t)
	var signed string
	f.h.JoinTLS = func(_ context.Context, id, csr string) (*cluster.PayloadTLS, error) {
		signed = id + " " + csr
		return &cluster.PayloadTLS{CACert: "CA", CASHA256: "ab", Cert: "CERT"}, nil
	}
	f.openJoins()
	var body map[string]any
	_ = json.Unmarshal([]byte(joinBody(nodeID, nodePubB64)), &body)
	body["csr"] = "-----BEGIN CERTIFICATE REQUEST-----\nx\n-----END CERTIFICATE REQUEST-----\n"
	b, _ := json.Marshal(body)
	var jr JoinResponse
	decode(t, f.do("POST", "/api/cluster/join", string(b), nil), http.StatusAccepted, &jr)
	decode(t, f.approve(nodeCode(t, jr.HomePubkey)), 200, nil)
	var pr PollResponse
	decode(t, f.poll(nodePubB64), 200, &pr)
	priv, _ := cluster.DecodeKey(nodePrivB64)
	nodePub, _ := cluster.DecodeKey(nodePubB64)
	homePub, _ := cluster.DecodeKey(jr.HomePubkey)
	shared, _ := cluster.SharedSecret(priv, homePub)
	key, _ := cluster.SealKey(shared, nodePub, homePub)
	plain, err := cluster.Open(key, nodeID, *pr.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	var p cluster.Payload
	if err := json.Unmarshal(plain, &p); err != nil {
		t.Fatal(err)
	}
	if p.TLS == nil || p.TLS.Cert != "CERT" || p.TLS.CACert != "CA" || signed == "" {
		t.Fatalf("payload tls %+v (signed %q)", p.TLS, signed)
	}
}
