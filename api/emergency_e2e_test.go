package main

// An emergency, end to end on one PC (specs/epics/37-node-replica, "Emergency
// promotion"), on the three machines of switch_e2e_helpers_test.go. The admin marks B a
// standby home on A: B gets A's kit, C learns where the standbys are. A is switched
// off. On B, its admin makes B the home (A's name typed to confirm): B is the home with
// A's secrets, C finds the new home at B by itself and follows it. A comes back, takes
// one more change as a home, then sees the newer term: it stops writing, reports the
// change to B, sealed, and becomes one of B's machines. On B the admin reviews the
// report and applies it.
//
// Opt-in: MAKTABA_E2E_SWITCHOVER=1 and MAKTABA_TEST_PG_BIN, as TestSwitchTheHome.

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAnEmergencyTakesTheHome(t *testing.T) {
	c := newSwitchCluster(t, false)
	ctx := c.ctx
	a, b, cc := c.a, c.b, c.c

	// ---- B, a standby home: it gets A's kit; C learns where the standbys are ----
	admin, csrf := c.signIn(a.webURL)
	if r, body := admin.do("PUT", a.webURL+"/api/cluster/switchover/standby",
		map[string]any{"node_id": c.nodeB, "standby": true}, csrf); r.StatusCode != 200 {
		t.Fatalf("mark B a standby home: %d %s", r.StatusCode, tail(string(body), 300))
	}
	waitFor(t, "B holds A's kit", 4*time.Minute, func() bool {
		_, body := admin.try("GET", a.webURL+"/api/cluster/switchover", nil, nil)
		var s struct {
			Standbys []struct {
				NodeID string  `json:"node_id"`
				KitAt  *string `json:"kit_at"`
			} `json:"standbys"`
		}
		_ = json.Unmarshal(body, &s)
		for _, sb := range s.Standbys {
			if sb.NodeID == c.nodeB && sb.KitAt != nil {
				return true
			}
		}
		return false
	})
	if _, err := os.Stat(filepath.Join(b.dir, "standby-kit.chunks")); err != nil {
		t.Fatalf("B keeps no kit: %v", err)
	}
	waitFor(t, "C knows where the standby homes are", 4*time.Minute, func() bool {
		raw, _ := os.ReadFile(filepath.Join(cc.dir, "node.json"))
		return strings.Contains(string(raw), b.webURL)
	})

	// ---- A is gone: B's admin makes B the home ----
	c.ma.stop()
	onB, csrfB := c.signIn(b.webURL)
	var em string
	waitFor(t, "B can take over", 3*time.Minute, func() bool {
		_, body := onB.try("GET", b.webURL+"/api/cluster/emergency", nil, nil)
		em = string(body)
		var s struct {
			Ready bool `json:"ready"`
		}
		return json.Unmarshal(body, &s) == nil && s.Ready
	})
	t.Logf("B before taking over: %s", tail(em, 400))
	if code, _ := onB.try("POST", b.webURL+"/api/cluster/emergency", map[string]string{"confirm": "not the home"}, csrfB); code == 202 {
		t.Fatal("a wrong name confirmed the emergency")
	}
	if code, body := onB.try("POST", b.webURL+"/api/cluster/emergency", map[string]string{"confirm": "a"}, csrfB); code != 202 {
		t.Fatalf("take over: %d %s", code, tail(string(body), 300))
	}
	waitFor(t, "B the home", 6*time.Minute, func() bool {
		s := c.selfOf(b.webURL)
		return s.Role == "home" && s.Term == 2
	})
	// C finds the new home at the standby, and follows it (copying it anew)
	waitFor(t, "C follows B by itself", 10*time.Minute, func() bool {
		s := c.selfOf(cc.webURL)
		return s.Role == "node" && s.Home != nil && s.Home.WebURL == b.webURL && s.Copy.State == "following"
	})
	bdb, err := sql.Open("postgres", c.appDSN(b))
	if err != nil {
		t.Fatal(err)
	}
	defer bdb.Close()
	var secretsOnB int
	_ = bdb.QueryRowContext(ctx, `SELECT count(*) FROM home_secrets WHERE name = 'ext.plex.token'`).Scan(&secretsOnB)
	if secretsOnB != 1 {
		t.Fatal("the home's secrets did not come with the standby's kit")
	}

	// ---- A comes back: one change as a home, then it gives the home up ----
	c.ma = startMachine(t, "a2", c.bin, c.env(a, c.aCfg))
	adb, err := sql.Open("postgres", c.appDSN(a))
	if err != nil {
		t.Fatal(err)
	}
	defer adb.Close()
	waitFor(t, "a change on A, back", 3*time.Minute, func() bool {
		_, err := adb.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at)
		    VALUES ('test.kept', '"written on a after the cut"', now())`)
		return err == nil
	})
	waitFor(t, "A one of B's machines", 10*time.Minute, func() bool {
		s := c.selfOf(a.webURL)
		return s.Role == "node" && s.Home != nil && s.Home.WebURL == b.webURL
	})

	// ---- on B: A's report, reviewed and applied ----
	home, csrfHome := c.signIn(b.webURL)
	var reportID string
	waitFor(t, "A's report on B", 3*time.Minute, func() bool {
		_, body := home.try("GET", b.webURL+"/api/cluster/switchover", nil, nil)
		var s struct {
			Reports []struct {
				ID      string `json:"id"`
				Changes int    `json:"changes"`
			} `json:"reports"`
		}
		_ = json.Unmarshal(body, &s)
		if len(s.Reports) == 1 && s.Reports[0].Changes > 0 {
			reportID = s.Reports[0].ID
			return true
		}
		return false
	})
	code, body := home.try("GET", b.webURL+"/api/cluster/kept-aside/"+reportID+"/plan", nil, nil)
	var plan struct {
		Hash         string `json:"hash"`
		Apply        int    `json:"apply"`
		Transactions []struct {
			Tables []string `json:"tables"`
			Apply  bool     `json:"apply"`
			Reason string   `json:"reason"`
		} `json:"transactions"`
	}
	if code != 200 || json.Unmarshal(body, &plan) != nil {
		t.Fatalf("the plan: %d %s", code, tail(string(body), 400))
	}
	settingApplies := false
	for _, tx := range plan.Transactions {
		for _, tb := range tx.Tables {
			if tb == "public.app_settings" && tx.Apply {
				settingApplies = true
			}
			if strings.HasPrefix(tb, "public.cluster_") && tx.Apply {
				t.Fatalf("a change of the cluster's own rows would be applied: %+v", tx)
			}
		}
	}
	if !settingApplies {
		t.Fatalf("A's change does not apply: %s", tail(string(body), 1500))
	}
	if code, body := home.try("POST", b.webURL+"/api/cluster/kept-aside/"+reportID+"/apply",
		map[string]string{"plan": plan.Hash}, csrfHome); code != 200 {
		t.Fatalf("apply: %d %s", code, tail(string(body), 400))
	}
	var v string
	_ = bdb.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = 'test.kept'`).Scan(&v)
	if !strings.Contains(v, "written on a after the cut") {
		t.Fatalf("A's change is not on B after applying: %q", v)
	}
	// the change reaches the machines too: A's own copy of B
	waitFor(t, "A's copy of B following, with its change back", 10*time.Minute, func() bool {
		s := c.selfOf(a.webURL)
		return s.Copy.State == "following"
	})
}
