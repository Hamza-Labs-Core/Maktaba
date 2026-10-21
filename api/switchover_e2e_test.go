package main

// Switching the home, end to end on one PC (specs/epics/37-node-replica, "Switching the
// home"): three `maktaba-server serve` machines on 127.0.0.1 (switch_e2e_helpers_test.go)
// — a home A on its own bundled Postgres with the cloud relay agent, and two full machines
// B and C whose supervisors keep copies of it — and an admin who asks A to hand over to
// B, with C off. Then, with no step by hand: B is the home (its copy promoted, A's
// secrets key, cluster key and relay credentials with it, its Postgres the home's, its
// relay agent running and A's stopped), A is one of its machines (its database a copy of
// B's without copying again, its node.json written), both started again in their new
// parts; C follows B by itself once back; A's secret opens on B, a change made on B
// reaches A, the owner signs in on either, and B calls the switch done once A follows.
//
// Opt-in: MAKTABA_E2E_SWITCHOVER=1 and MAKTABA_TEST_PG_BIN (a Postgres 16 install); it
// builds the three binaries. Every path is under t.TempDir().

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

func TestSwitchTheHome(t *testing.T) {
	c := newSwitchCluster(t, true)
	ctx := c.ctx
	a, b, cc := c.a, c.b, c.c
	nodeID := c.nodeB
	waitFor(t, "A's cloud relay agent", time.Minute, func() bool { return c.relayUp(a) })
	if c.relayUp(b) {
		t.Fatal("a machine runs the home's relay agent")
	}
	// C is off during the switch: it learns of the new home when it is back
	c.mc.stop()

	// ---- the admin asks A to hand over to B ----
	admin, csrf := c.signIn(a.webURL)
	type status struct {
		Candidates []struct {
			NodeID  string   `json:"node_id"`
			Ready   bool     `json:"ready"`
			Reasons []string `json:"reasons"`
		} `json:"candidates"`
	}
	var last string
	waitFor(t, "B ready to take over", 3*time.Minute, func() bool {
		_, body := admin.try("GET", a.webURL+"/api/cluster/switchover", nil, nil)
		last = string(body)
		var s status
		_ = json.Unmarshal(body, &s)
		for _, m := range s.Candidates {
			if m.NodeID == nodeID {
				return m.Ready
			}
		}
		return false
	})
	t.Logf("before the switch: %s", tail(last, 600))
	r, body := admin.do("POST", a.webURL+"/api/cluster/switchover", map[string]string{"to_node": nodeID}, csrf)
	if r.StatusCode != 202 {
		t.Fatalf("start the switch: %d %s", r.StatusCode, tail(string(body), 400))
	}
	// the switch as A tells it, for a run that fails
	var onA string
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the switch on A, last seen: %s", tail(onA, 2000))
		}
	})
	seeA := func() {
		if _, body := admin.try("GET", a.webURL+"/api/cluster/switchover", nil, nil); len(body) > 0 {
			onA = string(body)
		}
	}
	waitFor(t, "B the home", 6*time.Minute, func() bool { seeA(); return c.selfOf(b.webURL).Role == "home" })
	waitFor(t, "A one of B's machines", 6*time.Minute, func() bool {
		seeA()
		s := c.selfOf(a.webURL)
		return s.Role == "node" && s.Home != nil && s.Home.WebURL == b.webURL
	})
	// the relay moved with the home: B runs it with A's credentials, A runs none
	waitFor(t, "B's cloud relay agent", 2*time.Minute, func() bool { return c.relayUp(b) })
	if c.relayUp(a) {
		t.Fatal("the old home still runs the relay agent")
	}
	if cred, err := os.ReadFile(filepath.Join(b.dir, "cloudlink.cred")); err != nil || string(cred) != "sealed credentials" {
		t.Fatalf("the relay's credentials on the new home: %q %v", cred, err)
	}
	if _, err := os.Stat(filepath.Join(a.dir, "cloudlink.cred")); err == nil {
		t.Fatal("the old home keeps the relay's credentials")
	}

	// ---- C, back: it asks A, which hands it the new home's notice sealed for it ----
	c.mc = startMachine(t, "c2", c.bin, c.env(cc, c.cCfg))
	waitFor(t, "C follows B by itself", 8*time.Minute, func() bool {
		s := c.selfOf(cc.webURL)
		return s.Role == "node" && s.Home != nil && s.Home.WebURL == b.webURL && s.Copy.State == "following"
	})
	cNode, err := os.ReadFile(filepath.Join(cc.dir, "node.json"))
	if err != nil || !strings.Contains(string(cNode), fmt.Sprintf(":%d", b.pgPort)) {
		t.Fatalf("C's node.json does not name B's database: %s %v", cNode, err)
	}
	if code, _ := c.get(fmt.Sprintf("%s/api/cluster/moved/%s", a.webURL, uuid.NewString())); code != http.StatusNotFound {
		t.Fatalf("a notice for a machine that has none: %d", code)
	}

	// ---- after: the secret opens on B, B's change reaches A, the owner signs in on both ----
	bdb, err := sql.Open("postgres", c.appDSN(b))
	if err != nil {
		t.Fatal(err)
	}
	defer bdb.Close()
	var sealed, n []byte
	var keyID string
	if err := bdb.QueryRowContext(ctx, `SELECT ciphertext, nonce, key_id FROM home_secrets WHERE name = 'ext.plex.token'`).
		Scan(&sealed, &n, &keyID); err != nil {
		t.Fatalf("the secret on the new home: %v", err)
	}
	bKeyRaw, _ := os.ReadFile(filepath.Join(b.dir, "secrets.key"))
	bKey, err := secrets.NewKey(bKeyRaw)
	if err != nil {
		t.Fatalf("the new home's secrets key: %v", err)
	}
	if plain, err := secrets.Open(bKey, "ext.plex.token", sealed, n); err != nil || string(plain) != "plex-token-value" {
		t.Fatalf("the secret opens on the new home: %q %v", plain, err)
	}
	if _, err := bdb.ExecContext(ctx, `UPDATE app_settings SET value = '"on b"' WHERE key = 'test.switch'`); err != nil {
		t.Fatalf("a change on the new home: %v", err)
	}
	asuper, err := sql.Open("postgres", c.superDSN(a))
	if err != nil {
		t.Fatal(err)
	}
	defer asuper.Close()
	waitFor(t, "B's change on A's copy", 2*time.Minute, func() bool {
		var v string
		_ = asuper.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = 'test.switch'`).Scan(&v)
		return strings.Contains(v, "on b")
	})
	var aSecrets int
	_ = asuper.QueryRowContext(ctx, `SELECT count(*) FROM home_secrets`).Scan(&aSecrets)
	if aSecrets != 0 {
		t.Fatalf("the old home kept %d secrets", aSecrets)
	}
	onB, _ := c.signIn(b.webURL)
	var done string
	waitFor(t, "the switch done on the new home", 5*time.Minute, func() bool {
		_, body := onB.try("GET", b.webURL+"/api/cluster/switchover", nil, nil)
		done = string(body)
		var s struct {
			Recent []struct {
				State string                     `json:"state"`
				Integ map[string]json.RawMessage `json:"integrations"`
			} `json:"recent"`
			Term struct {
				Term int64 `json:"term"`
			} `json:"term"`
		}
		_ = json.Unmarshal(body, &s)
		return len(s.Recent) > 0 && s.Recent[0].State == "done" && s.Term.Term == 2 &&
			strings.Contains(string(s.Recent[0].Integ["secrets"]), `"moved"`) &&
			strings.Contains(string(s.Recent[0].Integ["relay"]), `"moved"`)
	})
	t.Logf("the switch on the new home: %s", tail(done, 900))
	waitFor(t, "a sign-in on the old home, now a machine", 2*time.Minute, func() bool {
		code, _ := newBrowser(t).try("POST", a.webURL+"/api/auth/login",
			map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
		return code == 200
	})
	if hold := os.Getenv("MAKTABA_E2E_HOLD"); hold != "" {
		var secs int
		_, _ = fmt.Sscanf(hold, "%d", &secs)
		time.Sleep(time.Duration(secs) * time.Second)
	}
}

// try is do that does not fail the test when the machine does not answer (it may be
// starting again in its new part): status 0 then.
func (b *browser) try(method, u string, body any, hdr map[string]string) (int, []byte) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return 0, nil
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := b.c.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}
