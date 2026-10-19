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
	NodeStaticPrivate string        `json:"node_static_private"`
	NodeStaticPublic  string        `json:"node_static_public"`
	ClientEphPrivate  string        `json:"client_eph_private"`
	ClientEphPublic   string        `json:"client_eph_public"`
	ServerEphPrivate  string        `json:"server_eph_private"`
	ServerEphPublic   string        `json:"server_eph_public"`
	HomeStaticPrivate string        `json:"home_static_private"`
	HomeStaticPublic  string        `json:"home_static_public"`
	ClientNonce       string        `json:"client_nonce"`
	ServerNonce       string        `json:"server_nonce"`
	Hello             string        `json:"hello"`
	Challenge         string        `json:"challenge"`
	TranscriptHash    string        `json:"transcript_hash"`
	DHEE              string        `json:"dh_ee"`
	DHSE              string        `json:"dh_se"`
	DHES              string        `json:"dh_es"`
	KC2S              string        `json:"k_c2s"`
	KS2C              string        `json:"k_s2c"`
	ClientProof       string        `json:"client_proof"`
	ServerProof       string        `json:"server_proof"`
	Frames            []vectorFrame `json:"frames"`
	Home              struct {
		HomeKey        string `json:"home_key"`
		Hello          string `json:"hello"`
		Challenge      string `json:"challenge"`
		TranscriptHash string `json:"transcript_hash"`
		KC2S           string `json:"k_c2s"`
		KS2C           string `json:"k_s2c"`
		ClientProof    string `json:"client_proof"`
		ServerProof    string `json:"server_proof"`
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

func pubOf(t *testing.T, priv string) []byte {
	t.Helper()
	p, err := cluster.PublicKey(b64(t, priv))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVectorsNode(t *testing.T) {
	v := loadVectors(t)
	for priv, pub := range map[string]string{
		v.NodeStaticPrivate: v.NodeStaticPublic, v.ClientEphPrivate: v.ClientEphPublic,
		v.ServerEphPrivate: v.ServerEphPublic, v.HomeStaticPrivate: v.HomeStaticPublic,
	} {
		if !bytes.Equal(pubOf(t, priv), b64(t, pub)) {
			t.Fatalf("public key of %s", priv)
		}
	}
	// the hub's side, from its own private halves
	ee, err1 := DH(b64(t, v.ServerEphPrivate), b64(t, v.ClientEphPublic))
	se, err2 := DH(b64(t, v.ServerEphPrivate), b64(t, v.NodeStaticPublic))
	es, err3 := DH(b64(t, v.HomeStaticPrivate), b64(t, v.ClientEphPublic))
	if err := errors.Join(err1, err2, err3); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ee, b64(t, v.DHEE)) || !bytes.Equal(se, b64(t, v.DHSE)) || !bytes.Equal(es, b64(t, v.DHES)) {
		t.Fatal("dh mismatch")
	}
	// the machine's side reaches the same values
	ee2, _ := DH(b64(t, v.ClientEphPrivate), b64(t, v.ServerEphPublic))
	se2, _ := DH(b64(t, v.NodeStaticPrivate), b64(t, v.ServerEphPublic))
	es2, _ := DH(b64(t, v.ClientEphPrivate), b64(t, v.HomeStaticPublic))
	if !bytes.Equal(ee, ee2) || !bytes.Equal(se, se2) || !bytes.Equal(es, es2) {
		t.Fatal("the two sides disagree")
	}
	th := Transcript([]byte(v.Hello), []byte(v.Challenge))
	if !bytes.Equal(th, b64(t, v.TranscriptHash)) {
		t.Fatal("transcript mismatch")
	}
	c2s, s2c, err := DeriveKeys(NodeIKM(ee, se, es), b64(t, v.ClientNonce), b64(t, v.ServerNonce))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c2s, b64(t, v.KC2S)) || !bytes.Equal(s2c, b64(t, v.KS2C)) {
		t.Fatal("keys mismatch")
	}
	if !bytes.Equal(ClientProof(c2s, th), b64(t, v.ClientProof)) || !bytes.Equal(ServerProof(s2c, th), b64(t, v.ServerProof)) {
		t.Fatal("proof mismatch")
	}
	if !CheckClientProof(c2s, th, b64(t, v.ClientProof)) || CheckClientProof(s2c, th, b64(t, v.ClientProof)) {
		t.Fatal("CheckClientProof")
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
	// the welcome carries the server proof
	var w map[string]any
	_ = json.Unmarshal([]byte(v.Frames[0].Plaintext), &w)
	if w["t"] != "welcome" || w["proof"] != v.ServerProof {
		t.Fatalf("welcome = %v", w)
	}
}

func TestVectorsHome(t *testing.T) {
	v := loadVectors(t)
	ee, err := DH(b64(t, v.ServerEphPrivate), b64(t, v.ClientEphPublic))
	if err != nil {
		t.Fatal(err)
	}
	th := Transcript([]byte(v.Home.Hello), []byte(v.Home.Challenge))
	if !bytes.Equal(th, b64(t, v.Home.TranscriptHash)) {
		t.Fatal("home transcript mismatch")
	}
	c2s, s2c, err := DeriveKeys(HomeIKM(b64(t, v.Home.HomeKey), ee), b64(t, v.ClientNonce), b64(t, v.ServerNonce))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c2s, b64(t, v.Home.KC2S)) || !bytes.Equal(s2c, b64(t, v.Home.KS2C)) {
		t.Fatal("home keys mismatch")
	}
	if !bytes.Equal(ClientProof(c2s, th), b64(t, v.Home.ClientProof)) || !bytes.Equal(ServerProof(s2c, th), b64(t, v.Home.ServerProof)) {
		t.Fatal("home proof mismatch")
	}
}

func TestDHRefusesLowOrder(t *testing.T) {
	priv := bytes.Repeat([]byte{9}, 32)
	if _, err := DH(priv, make([]byte, 32)); !errors.Is(err, ErrLowOrder) {
		t.Fatalf("zero point accepted: %v", err)
	}
	if _, err := DH(priv, []byte{1}); err == nil {
		t.Fatal("short key accepted")
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
			t.Fatal("short ikm accepted")
		}
		if _, _, err := DeriveKeys(key, make([]byte, 15), make([]byte, 16)); err == nil {
			t.Fatal("short nonce accepted")
		}
	})
}
