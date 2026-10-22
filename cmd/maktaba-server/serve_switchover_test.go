package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/nodecopy"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "home database CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// The home hands its database CA over the machine channel: node.json keeps it (and
// what else the file has), the machine starts again with it; the same CA again changes
// nothing; a CA that is not one is refused.
func TestHomeDatabaseOverTheChannel(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node.json")
	t.Setenv("MAKTABA_NODE_CONFIG", node)
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	if err := os.WriteFile(node, []byte(`{"node_id":"n1","name":"studio","private_key":"k",
	  "home":{"url":"http://home:8080"},"database_url":"postgres://r:p@home:54329/maktaba?sslmode=prefer",
	  "joined_at":"2026-10-01T10:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for len(reloadCh) > 0 {
		<-reloadCh
	}
	s := newSwitching(config.Config{})
	ca := testCAPEM(t)
	args, _ := json.Marshal(map[string]string{"ca_pem": ca})
	handled, ok, detail := s.command(t.Context(), "home.database", args)
	if !handled || !ok {
		t.Fatalf("the CA: %v %v", ok, detail)
	}
	n := config.LocalNodeConfig()
	if n.DatabaseCA != ca || !n.DatabaseVerified() {
		t.Fatalf("node.json: %+v", n)
	}
	var raw map[string]any
	b, _ := os.ReadFile(node)
	_ = json.Unmarshal(b, &raw)
	if raw["joined_at"] != "2026-10-01T10:00:00Z" {
		t.Fatalf("node.json lost the pipeline's own fields: %s", b)
	}
	select {
	case <-reloadCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the machine does not start again with the CA")
	}
	if _, ok, detail := s.command(t.Context(), "home.database", args); !ok || detail.(map[string]any)["changed"] != false {
		t.Fatalf("the same CA again: %v %v", ok, detail)
	}
	bad, _ := json.Marshal(map[string]string{"ca_pem": "not a certificate"})
	if _, ok, _ := s.command(t.Context(), "home.database", bad); ok {
		t.Fatal("a CA that is not one was taken")
	}
	// the home's database without TLS: plain, on its word
	off, _ := json.Marshal(map[string]bool{"tls_off": true})
	if _, ok, _ := s.command(t.Context(), "home.database", off); !ok {
		t.Fatal("TLS off")
	}
	if n := config.LocalNodeConfig(); n.DatabaseCA != "" || n.DatabaseTLS != "off" {
		t.Fatalf("node.json with TLS off: %+v", n)
	}
}

// homeDirs isolates a machine's files: server.toml and the keys beside it, its state, the
// home's secrets and cluster keys.
func homeDirs(t *testing.T) (*switching, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	t.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(dir, "node.json"))
	t.Setenv("MAKTABA_SECRETS_KEY", filepath.Join(dir, "secrets.key"))
	t.Setenv("MAKTABA_HOME_CLUSTER_KEY", filepath.Join(dir, "home-cluster.key"))
	s := newSwitching(config.Config{Server: config.ServerSection{DataDir: filepath.Join(dir, "state")}})
	s.set(nil, nil, nil, "", nil)
	return s, dir
}

// The old home, its promotion's answer lost, calls a switch off at the machine taking over
// when it was not promoted there: from then its promotion never runs.
func TestASwitchCalledOffIsNeverPromoted(t *testing.T) {
	s, _ := homeDirs(t)
	ctx := t.Context()
	id := "4b8f7c1e-2a3d-4e5f-9a0b-1c2d3e4f5a6b"
	st := func(abort bool) string {
		t.Helper()
		out, err := s.status(ctx, id, 2, abort)
		if err != nil {
			t.Fatal(err)
		}
		return out.(map[string]any)["state"].(string)
	}
	if got := st(false); got != "not_promoted" {
		t.Fatalf("asked: %s", got)
	}
	if got := st(true); got != "called_off" {
		t.Fatalf("called off: %s", got)
	}
	args, _ := json.Marshal(map[string]any{"id": id, "term": 2})
	if handled, ok, detail := s.command(ctx, "switch.promote", args); !handled || ok {
		t.Fatalf("a switch called off was promoted: %v %v", ok, detail)
	}
	if got := st(false); got != "called_off" {
		t.Fatalf("asked again: %s", got)
	}
	// one promoted here answers what it answered
	other := "9d1e2f3a-4b5c-4d6e-8f70-81a2b3c4d5e6"
	if err := writePrivate(s.newHomeFile(other), []byte(`{"api_url":"http://b:8080","db_host":"b","db_port":54329}`)); err != nil {
		t.Fatal(err)
	}
	out, err := s.status(ctx, other, 2, true)
	if err != nil || out.(map[string]any)["state"] != "home" {
		t.Fatalf("a switch promoted here: %v %v", out, err)
	}
	if h, ok := s.promoted(other); !ok || h.APIURL != "http://b:8080" {
		t.Fatalf("its answer: %+v", h)
	}
}

// The API's record of a switch is kept where a restart finds it, and once ended there
// it is not taken up again.
func TestTheRecordOfASwitch(t *testing.T) {
	s, _ := homeDirs(t)
	id := "4b8f7c1e-2a3d-4e5f-9a0b-1c2d3e4f5a6b"
	if err := s.record(id, json.RawMessage(`{"id":"`+id+`","state":"promoting","promote_sent":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.record(id, json.RawMessage(`not json`)); err == nil {
		t.Fatal("a record that is not one was kept")
	}
	open := func() int {
		out, err := s.recorded()
		if err != nil {
			t.Fatal(err)
		}
		return len(out.(map[string]any)["switches"].([]json.RawMessage))
	}
	if n := open(); n != 1 {
		t.Fatalf("records: %d", n)
	}
	s.endRecord(id, "handed_over")
	if n := open(); n != 0 {
		t.Fatalf("an ended switch is taken up again: %d", n)
	}
}

// A machine that is not the home any more keeps none of the home's keys (nor the kit, nor
// what an earlier release set aside).
func TestTheOldHomeKeepsNoneOfTheHomesKeys(t *testing.T) {
	s, dir := homeDirs(t)
	id := "4b8f7c1e-2a3d-4e5f-9a0b-1c2d3e4f5a6b"
	paths := append(s.homeKeyFiles(), filepath.Join(dir, "home-cluster.key.was-home-1"),
		filepath.Join(s.dir(id), "kit.chunks"), filepath.Join(s.dir(id), "old-home.json"))
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("a home's secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(dir, "postgres-ca.crt") // the CA's certificate is public
	if err := os.WriteFile(keep, []byte("cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.wipeHomeKeys(id, t.Logf)
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s is still there: %v", p, err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the CA's certificate went: %v", err)
	}
	if err := wipeFile(filepath.Join(dir, "none")); err != nil {
		t.Fatalf("no file: %v", err)
	}
}

// A machine whose database does not answer (starting, recovering) does not know whether
// it promoted: it never calls the switch off then (the old home would take changes again
// beside a machine that may be the home), and a switch it called off is never finished.
func TestAnUnreadableCopyNeverCallsASwitchOff(t *testing.T) {
	s, dir := homeDirs(t)
	ctx := t.Context()
	keys := filepath.Join(dir, "copy-keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keys, "postgres-postgres.password"), []byte("pw"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.cp = &nodeCopy{copy: nodecopy.Copy{DB: pgbundle.Bundle{Data: filepath.Join(dir, "copy"), Keys: keys, Port: 1}}}
	id := "6e0b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d"
	if out, err := s.status(ctx, id, 2, true); err == nil {
		t.Fatalf("an unreadable copy answered %v", out)
	}
	if fileExists(s.calledOff(id)) {
		t.Fatal("called off on a copy that does not answer")
	}
	if err := writePrivate(s.calledOff(id), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishPromotion(ctx, id, 2); err == nil || !strings.Contains(err.Error(), "called off") {
		t.Fatalf("a promotion called off here was finished: %v", err)
	}
}

// A start drops the kits of switches over here (called off, or promoted and answered),
// and keeps one whose switch may still need it.
func TestAStartSweepsTheKitsOfSwitchesOver(t *testing.T) {
	s, _ := homeDirs(t)
	put := func(id string, markers ...string) string {
		t.Helper()
		kit := filepath.Join(s.dir(id), "kit.chunks")
		for _, p := range append([]string{kit}, markers...) {
			if err := writePrivate(p, []byte("x")); err != nil {
				t.Fatal(err)
			}
		}
		return kit
	}
	off := put("a1", s.calledOff("a1"))
	done := put("a2", s.newHomeFile("a2"))
	open := put("a3")
	s.sweepKits()
	if fileExists(off) || fileExists(done) || !fileExists(open) {
		t.Fatalf("after the sweep: called off %v, done %v, open %v", fileExists(off), fileExists(done), fileExists(open))
	}
}

// The term watch looks for a newer home under the standbys' identity and each planned
// switch's whose promotion was asked (one undone before has no notice anywhere).
func TestTheTermWatchLooksUnderEveryIdentity(t *testing.T) {
	s, _ := homeDirs(t)
	ident := func(path, id string) {
		t.Helper()
		b, _ := json.Marshal(oldHome{NodeID: id, PrivateKey: "AAAA", PublicKey: "AAAA"})
		if err := writePrivate(path, b); err != nil {
			t.Fatal(err)
		}
	}
	ident(s.standbyIdentityPath(), "standby-id")
	ident(filepath.Join(s.dir("asked"), "old-home.json"), "planned-asked")
	if err := writePrivate(filepath.Join(s.dir("asked"), "progress.json"), []byte(`{"promote_sent": true}`)); err != nil {
		t.Fatal(err)
	}
	ident(filepath.Join(s.dir("undone"), "old-home.json"), "planned-undone")
	if err := writePrivate(filepath.Join(s.dir("undone"), "progress.json"), []byte(`{"state": "failed"}`)); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, me := range s.homeIdentities() {
		got = append(got, me.NodeID)
	}
	if strings.Join(got, ",") != "standby-id,planned-asked" {
		t.Fatalf("identities: %v", got)
	}
}
