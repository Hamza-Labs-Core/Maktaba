package machineagent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type vectors struct {
	NodeStaticPrivate string `json:"node_static_private"`
	ClientEphPrivate  string `json:"client_eph_private"`
	ServerEphPublic   string `json:"server_eph_public"`
	HomeStaticPublic  string `json:"home_static_public"`
	ClientNonce       string `json:"client_nonce"`
	ServerNonce       string `json:"server_nonce"`
	Hello             string `json:"hello"`
	Challenge         string `json:"challenge"`
	TranscriptHash    string `json:"transcript_hash"`
	DHEE              string `json:"dh_ee"`
	DHSE              string `json:"dh_se"`
	DHES              string `json:"dh_es"`
	KC2S              string `json:"k_c2s"`
	KS2C              string `json:"k_s2c"`
	ClientProof       string `json:"client_proof"`
	ServerProof       string `json:"server_proof"`
	Frames            []struct {
		Dir       string `json:"dir"`
		Counter   uint64 `json:"counter"`
		Plaintext string `json:"plaintext"`
		Sealed    string `json:"sealed"`
	} `json:"frames"`
}

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TC5 of story 35.8: the machine agent's side of handshake v2 and sealing matches the
// vectors the hub (Go) and the pipeline's agent (Python) check.
func TestMachineAgentHandshakeVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "testdata", "cluster", "agent_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	th := transcript([]byte(v.Hello), []byte(v.Challenge))
	if !bytes.Equal(th, b64(t, v.TranscriptHash)) {
		t.Fatal("transcript hash differs")
	}
	ee, err := dh(b64(t, v.ClientEphPrivate), b64(t, v.ServerEphPublic))
	if err != nil || !bytes.Equal(ee, b64(t, v.DHEE)) {
		t.Fatalf("dh_ee: %v", err)
	}
	se, err := dh(b64(t, v.NodeStaticPrivate), b64(t, v.ServerEphPublic))
	if err != nil || !bytes.Equal(se, b64(t, v.DHSE)) {
		t.Fatalf("dh_se: %v", err)
	}
	es, err := dh(b64(t, v.ClientEphPrivate), b64(t, v.HomeStaticPublic))
	if err != nil || !bytes.Equal(es, b64(t, v.DHES)) {
		t.Fatalf("dh_es: %v", err)
	}
	ikm := append(append(append([]byte{}, ee...), se...), es...)
	c2s, s2c, err := deriveKeys(ikm, b64(t, v.ClientNonce), b64(t, v.ServerNonce))
	if err != nil || !bytes.Equal(c2s, b64(t, v.KC2S)) || !bytes.Equal(s2c, b64(t, v.KS2C)) {
		t.Fatalf("keys: %v", err)
	}
	if !bytes.Equal(clientProof(c2s, th), b64(t, v.ClientProof)) || !bytes.Equal(serverProof(s2c, th), b64(t, v.ServerProof)) {
		t.Fatal("proofs differ")
	}
	sealC, _ := newCipher(c2s)
	openS, _ := newCipher(s2c)
	for _, f := range v.Frames {
		switch f.Dir {
		case "c2s":
			got, err := sealC.seal([]byte(f.Plaintext))
			if err != nil || !bytes.Equal(got, b64(t, f.Sealed)) {
				t.Fatalf("c2s frame %d differs", f.Counter)
			}
		case "s2c":
			pt, err := openS.open(b64(t, f.Sealed))
			if err != nil || string(pt) != f.Plaintext {
				t.Fatalf("s2c frame %d: %v", f.Counter, err)
			}
		}
	}
	// a replayed frame does not open
	if len(v.Frames) > 0 && v.Frames[0].Dir == "s2c" {
		if _, err := openS.open(b64(t, v.Frames[0].Sealed)); err == nil {
			t.Fatal("a repeated frame must not open")
		}
	}
}
