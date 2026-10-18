package cluster

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type vectors struct {
	NodePrivate string   `json:"node_private"`
	NodePublic  string   `json:"node_public"`
	HomePrivate string   `json:"home_private"`
	HomePublic  string   `json:"home_public"`
	Shared      string   `json:"shared"`
	SAS         string   `json:"sas"`
	SealKey     string   `json:"seal_key"`
	NodeID      string   `json:"node_id"`
	Payload     string   `json:"payload"`
	Sealed      Envelope `json:"sealed"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "testdata", "cluster", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func mustKey(t *testing.T, s string) []byte {
	t.Helper()
	k, err := DecodeKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestVectors(t *testing.T) {
	v := loadVectors(t)
	nodePriv, homePriv := mustKey(t, v.NodePrivate), mustKey(t, v.HomePrivate)
	nodePub, homePub := mustKey(t, v.NodePublic), mustKey(t, v.HomePublic)

	if got, _ := PublicKey(nodePriv); !bytes.Equal(got, nodePub) {
		t.Errorf("node public = %s", EncodeKey(got))
	}
	if got, _ := PublicKey(homePriv); !bytes.Equal(got, homePub) {
		t.Errorf("home public = %s", EncodeKey(got))
	}
	sharedHome, err := SharedSecret(homePriv, nodePub)
	if err != nil {
		t.Fatal(err)
	}
	sharedNode, _ := SharedSecret(nodePriv, homePub)
	if EncodeKey(sharedHome) != v.Shared || EncodeKey(sharedNode) != v.Shared {
		t.Fatalf("shared = %s / %s, want %s", EncodeKey(sharedHome), EncodeKey(sharedNode), v.Shared)
	}
	sas, err := SAS(sharedHome, nodePub, homePub)
	if err != nil || sas != v.SAS {
		t.Errorf("sas = %q (%v), want %q", sas, err, v.SAS)
	}
	key, err := SealKey(sharedHome, nodePub, homePub)
	if err != nil || base64.StdEncoding.EncodeToString(key) != v.SealKey {
		t.Errorf("seal key = %s (%v)", base64.StdEncoding.EncodeToString(key), err)
	}

	plain, err := Open(key, v.NodeID, v.Sealed)
	if err != nil || string(plain) != v.Payload {
		t.Fatalf("open = %q (%v)", plain, err)
	}
	nonce, _ := base64.StdEncoding.DecodeString(v.Sealed.Nonce)
	env, err := SealWithNonce(key, v.NodeID, nonce, []byte(v.Payload))
	if err != nil || env != v.Sealed {
		t.Errorf("seal = %+v (%v)", env, err)
	}
	// The AAD binds the node id.
	if _, err := Open(key, "00000000-0000-0000-0000-000000000000", v.Sealed); err == nil {
		t.Error("open with another node id must fail")
	}

	// Our payload encoding reproduces the vector's bytes.
	var p Payload
	if err := json.Unmarshal([]byte(v.Payload), &p); err != nil {
		t.Fatal(err)
	}
	enc, err := MarshalPayload(p)
	if err != nil || string(enc) != v.Payload {
		t.Errorf("payload = %s", enc)
	}
}

func TestSealRoundTripAndSASDiffersUnderSwap(t *testing.T) {
	nPriv, nPub, _ := GenerateKey()
	hPriv, hPub, _ := GenerateKey()
	mPriv, mPub, _ := GenerateKey() // a machine in the middle
	s1, _ := SharedSecret(hPriv, nPub)
	s2, _ := SharedSecret(nPriv, hPub)
	if !bytes.Equal(s1, s2) {
		t.Fatal("shared differs")
	}
	key, _ := SealKey(s1, nPub, hPub)
	env, err := Seal(key, "id", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Open(key, "id", env); err != nil || string(got) != "hello" {
		t.Fatalf("round trip %q %v", got, err)
	}
	// The middle machine talks to each side with its own key: the home
	// derives from (middle pub, home pub), the node from (node pub, middle pub).
	sh, _ := SharedSecret(hPriv, mPub)
	homeCode, _ := SAS(sh, mPub, hPub)
	sn, _ := SharedSecret(mPriv, nPub)
	nodeCode, _ := SAS(sn, nPub, mPub)
	if homeCode == nodeCode {
		t.Error("codes must differ under a key swap")
	}
}

func TestRoleNameAndURL(t *testing.T) {
	if got := RoleName("6F1C2A3B-4d5e-4f60-8a71-92b3c4d5e6f7"); got != "maktaba_node_6f1c2a3b" {
		t.Errorf("role = %s", got)
	}
	u := DatabaseURL("maktaba_node_6f1c2a3b", "s3cr3t", "192.168.1.20", 5432, "maktaba")
	if u != "postgres://maktaba_node_6f1c2a3b:s3cr3t@192.168.1.20:5432/maktaba?sslmode=prefer" {
		t.Errorf("url = %s", u)
	}
	if u := DatabaseURL("r", "p", "fe80::1", 5432, "m"); u != "postgres://r:p@[fe80::1]:5432/m?sslmode=prefer" {
		t.Errorf("v6 url = %s", u)
	}
}

func TestDecodeKeyRejects(t *testing.T) {
	if _, err := DecodeKey("AAAA"); err == nil {
		t.Error("short key accepted")
	}
	if _, err := DecodeKey("!!"); err == nil {
		t.Error("garbage accepted")
	}
}
