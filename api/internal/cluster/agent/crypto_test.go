package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

type vectorFrame struct {
	Dir       string `json:"dir"`
	Counter   uint64 `json:"counter"`
	Plaintext string `json:"plaintext"`
	Sealed    string `json:"sealed"`
}

type vectors struct {
	NodePrivate   string        `json:"node_private"`
	NodePublic    string        `json:"node_public"`
	ServerPrivate string        `json:"server_private"`
	ServerPublic  string        `json:"server_public"`
	ClientNonce   string        `json:"client_nonce"`
	ServerNonce   string        `json:"server_nonce"`
	Shared        string        `json:"shared"`
	KC2S          string        `json:"k_c2s"`
	KS2C          string        `json:"k_s2c"`
	Proof         string        `json:"proof"`
	Frames        []vectorFrame `json:"frames"`
	Home          struct {
		HomeKey string `json:"home_key"`
		KC2S    string `json:"k_c2s"`
		KS2C    string `json:"k_s2c"`
		Proof   string `json:"proof"`
	} `json:"home"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "testdata", "cluster", "agent_vectors.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVectorsNode(t *testing.T) {
	v := loadVectors(t)
	nodePub, err := cluster.PublicKey(b64(t, v.NodePrivate))
	if err != nil || !bytes.Equal(nodePub, b64(t, v.NodePublic)) {
		t.Fatalf("node public: %v", err)
	}
	serverPub, _ := cluster.PublicKey(b64(t, v.ServerPrivate))
	if !bytes.Equal(serverPub, b64(t, v.ServerPublic)) {
		t.Fatal("server public mismatch")
	}
	// both sides reach the same secret
	s1, _ := cluster.SharedSecret(b64(t, v.NodePrivate), serverPub)
	s2, _ := cluster.SharedSecret(b64(t, v.ServerPrivate), nodePub)
	if !bytes.Equal(s1, s2) || !bytes.Equal(s1, b64(t, v.Shared)) {
		t.Fatal("shared mismatch")
	}
	c2s, s2c, err := DeriveKeys(s1, b64(t, v.ClientNonce), b64(t, v.ServerNonce))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c2s, b64(t, v.KC2S)) || !bytes.Equal(s2c, b64(t, v.KS2C)) {
		t.Fatal("keys mismatch")
	}
	if !bytes.Equal(Proof(c2s, b64(t, v.ServerNonce)), b64(t, v.Proof)) {
		t.Fatal("proof mismatch")
	}
	if !CheckProof(c2s, b64(t, v.ServerNonce), b64(t, v.Proof)) || CheckProof(s2c, b64(t, v.ServerNonce), b64(t, v.Proof)) {
		t.Fatal("CheckProof")
	}

	// seal / open every frame at its counter, both ways
	keys := map[string][]byte{"c2s": c2s, "s2c": s2c}
	sealers := map[string]*Cipher{}
	openers := map[string]*Cipher{}
	for dir, k := range keys {
		sealers[dir], _ = NewCipher(k)
		openers[dir], _ = NewCipher(k)
	}
	for i, f := range v.Frames {
		if sealers[f.Dir].Counter() != f.Counter {
			t.Fatalf("frame %d: counter %d, want %d", i, sealers[f.Dir].Counter(), f.Counter)
		}
		got, err := sealers[f.Dir].Seal([]byte(f.Plaintext))
		if err != nil || !bytes.Equal(got, b64(t, f.Sealed)) {
			t.Fatalf("frame %d: seal mismatch (%v)", i, err)
		}
		pt, err := openers[f.Dir].Open(b64(t, f.Sealed))
		if err != nil || string(pt) != f.Plaintext {
			t.Fatalf("frame %d: open: %v", i, err)
		}
	}
}

func TestVectorsHome(t *testing.T) {
	v := loadVectors(t)
	c2s, s2c, err := DeriveKeys(b64(t, v.Home.HomeKey), b64(t, v.ClientNonce), b64(t, v.ServerNonce))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c2s, b64(t, v.Home.KC2S)) || !bytes.Equal(s2c, b64(t, v.Home.KS2C)) {
		t.Fatal("home keys mismatch")
	}
	if !bytes.Equal(Proof(c2s, b64(t, v.ServerNonce)), b64(t, v.Home.Proof)) {
		t.Fatal("home proof mismatch")
	}
}

func TestCipherRejectsReplayGapAndTamper(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	s, _ := NewCipher(key)
	f0, _ := s.Seal([]byte(`{"t":"ping"}`))
	f1, _ := s.Seal([]byte(`{"t":"pong"}`))
	f2, _ := s.Seal([]byte(`{"t":"ping"}`))

	t.Run("replay", func(t *testing.T) {
		o, _ := NewCipher(key)
		if _, err := o.Open(f0); err != nil {
			t.Fatal(err)
		}
		if _, err := o.Open(f0); !errors.Is(err, ErrFrame) {
			t.Fatalf("replay accepted: %v", err)
		}
		// broken for good, even for the right next frame
		if _, err := o.Open(f1); !errors.Is(err, ErrFrame) {
			t.Fatal("cipher not broken after a failure")
		}
	})
	t.Run("gap", func(t *testing.T) {
		o, _ := NewCipher(key)
		if _, err := o.Open(f0); err != nil {
			t.Fatal(err)
		}
		if _, err := o.Open(f2); !errors.Is(err, ErrFrame) {
			t.Fatal("gap accepted")
		}
	})
	t.Run("tamper", func(t *testing.T) {
		o, _ := NewCipher(key)
		bad := append([]byte{}, f0...)
		bad[0] ^= 1
		if _, err := o.Open(bad); !errors.Is(err, ErrFrame) {
			t.Fatal("tampered frame accepted")
		}
	})
	t.Run("bad inputs", func(t *testing.T) {
		if _, _, err := DeriveKeys([]byte{1}, make([]byte, 16), make([]byte, 16)); err == nil {
			t.Fatal("short shared accepted")
		}
		if _, _, err := DeriveKeys(key, make([]byte, 15), make([]byte, 16)); err == nil {
			t.Fatal("short nonce accepted")
		}
	})
}
