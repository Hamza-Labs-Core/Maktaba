package switchover

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/crypto/curve25519"
)

// An old home's report of what it kept aside opens with the cluster key and that old
// home's public key only, for its term.
func TestAReportOpensForTheClusterOnly(t *testing.T) {
	clusterPriv, clusterPub := keypair(t)
	oldPriv, oldPub := keypair(t)
	_, strangerPub := keypair(t)
	report := []byte(`{"term":3,"aside":{"transactions":[]}}`)
	sealed, err := SealReport(oldPriv, clusterPub, 3, report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "aside") {
		t.Fatal("the report is in the clear")
	}
	if got, err := OpenReport(clusterPriv, oldPub, 3, sealed); err != nil || !bytes.Equal(got, report) {
		t.Fatalf("the new home: %q %v", got, err)
	}
	if _, err := OpenReport(clusterPriv, strangerPub, 3, sealed); err == nil {
		t.Fatal("it opened as another machine's")
	}
	if _, err := OpenReport(clusterPriv, oldPub, 4, sealed); err == nil {
		t.Fatal("it opened for another term")
	}
}

// The new home's API opens what this sealed (api/internal/cluster/switchover's
// openReport): the vector both test against.
func TestTheReportVectorOpens(t *testing.T) {
	got, err := OpenReport(reportVectorCluster, reportVectorOldPub(t), 7, reportVectorSealed)
	if err != nil || string(got) != reportVectorPlain {
		t.Fatalf("the vector: %q %v", got, err)
	}
}

// The vector's keys: the cluster's private key, the old home's private key (its public
// half derived), the sealed report of term 7.
var (
	reportVectorCluster = bytes.Repeat([]byte{7}, 32)
	reportVectorOld     = bytes.Repeat([]byte{9}, 32)
)

const reportVectorPlain = `{"id":"r","term":7}`

// reportVectorSealed was made by SealReport(reportVectorOld, pub(reportVectorCluster), 7,
// reportVectorPlain).
const reportVectorSealed = "1CtyYpAaNuKahUBwDeKXtISXnI+U53Wzqy5D7yJ4wjXMbqo0lqakMa5cbbYpWgI="

func reportVectorOldPub(t *testing.T) []byte {
	t.Helper()
	pub, err := curve25519.X25519(reportVectorOld, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// A standby's kit is sent again when what it must hold changed (a secret, a key file, a
// machine, a role), not for the sessions and devices that ride along.
func TestAStandbyKitIsStaleWhenItsSecretsChange(t *testing.T) {
	kit := func() *Kit {
		return &Kit{Term: 2, Files: map[string][]byte{"secrets.key": []byte("k1")},
			Roles: []Role{{Name: "maktaba_node_a", Verifier: "SCRAM-SHA-256$v1"}},
			Tables: map[string][]json.RawMessage{
				"home_secrets":  {json.RawMessage(`{"name":"plex","ciphertext":"x"}`)},
				"cluster_nodes": {json.RawMessage(`{"id":"a","state":"approved","node_pubkey":"p","replica":{"lag":1}}`)},
				"web_sessions":  {json.RawMessage(`{"id":"s1"}`)},
			}}
	}
	base := kit().StableHash()
	if kit().StableHash() != base {
		t.Fatal("the same kit hashes differently")
	}
	k := kit()
	k.Tables["web_sessions"] = append(k.Tables["web_sessions"], json.RawMessage(`{"id":"s2"}`))
	k.Tables["cluster_nodes"] = []json.RawMessage{json.RawMessage(`{"id":"a","state":"approved","node_pubkey":"p","replica":{"lag":9}}`)}
	if k.StableHash() != base {
		t.Fatal("a new session or a machine's report made the kit stale")
	}
	for name, change := range map[string]func(*Kit){
		"a secret":   func(k *Kit) { k.Tables["home_secrets"][0] = json.RawMessage(`{"name":"plex","ciphertext":"y"}`) },
		"a key file": func(k *Kit) { k.Files["secrets.key"] = []byte("k2") },
		"a role":     func(k *Kit) { k.Roles[0].Verifier = "SCRAM-SHA-256$v2" },
		"a machine": func(k *Kit) {
			k.Tables["cluster_nodes"][0] = json.RawMessage(`{"id":"a","state":"revoked","node_pubkey":"p"}`)
		},
		"the term":       func(k *Kit) { k.Term = 3 },
		"the old home's": func(k *Kit) { k.OldHome = &Machine{ID: "o", PublicKey: "q"} },
	} {
		k := kit()
		change(k)
		if k.StableHash() == base {
			t.Errorf("%s changed, yet the kit is not stale", name)
		}
	}
}
