package switchover

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

func calledWith(h *fakeHub, prefix string) int {
	n := 0
	for _, c := range h.called() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// A standby home gets the home's kit, again only when it changed, and loses it when the
// mark is removed; every machine is told where the standbys are.
func TestAStandbyHomeKeepsTheHomesKit(t *testing.T) {
	o, h, s, b, c := setup(t)
	ctx := context.Background()
	h.nodes = []string{b, c}
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET web_url = 'http://' || name || ':8780'`); err != nil {
		t.Fatal(err)
	}
	if err := o.SetStandby(ctx, uuid.NewString(), true); !errors.Is(err, ErrNotStandby) {
		t.Fatalf("an unknown machine marked: %v", err)
	}
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET full_node = false WHERE id = $1`, c); err != nil {
		t.Fatal(err)
	}
	if err := o.SetStandby(ctx, c, true); !errors.Is(err, ErrNotStandby) {
		t.Fatalf("a worker-only machine marked: %v", err)
	}
	if err := o.SetStandby(ctx, b, true); err != nil {
		t.Fatal(err)
	}
	s.standbyHash = "kit-1"
	told := map[string]string{}
	if err := o.standbysOnce(ctx, told); err != nil {
		t.Fatal(err)
	}
	if calledWith(h, "standby.kit@s-"+b) != 1 || calledWith(h, "standby.commit@s-"+b) != 1 {
		t.Fatalf("the kit was not sent: %v", h.called())
	}
	if calledWith(h, "home.standbys@s-"+b) != 1 || calledWith(h, "home.standbys@s-"+c) != 1 {
		t.Fatalf("the machines were not told where the standbys are: %v", h.called())
	}
	ss, err := o.Standbys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var held *StandbyState
	for i := range ss {
		if ss[i].NodeID == b {
			held = &ss[i]
		}
	}
	if held == nil || !held.Standby || held.KitAt == nil || held.KitTerm != 2 {
		t.Fatalf("the standby: %+v", ss)
	}
	// nothing changed: nothing sent, nobody told again
	if err := o.standbysOnce(ctx, told); err != nil {
		t.Fatal(err)
	}
	if calledWith(h, "standby.kit@") != 1 || calledWith(h, "home.standbys@") != 2 {
		t.Fatalf("sent again with nothing changed: %v", h.called())
	}
	// a secret changed: the kit again
	s.standbyHash = "kit-2"
	if err := o.standbysOnce(ctx, told); err != nil {
		t.Fatal(err)
	}
	if calledWith(h, "standby.commit@s-"+b) != 2 {
		t.Fatalf("a changed kit was not sent: %v", h.called())
	}
	// the mark removed: the kit removed, the machines told
	if err := o.SetStandby(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	if err := o.standbysOnce(ctx, told); err != nil {
		t.Fatal(err)
	}
	var kit []byte
	_ = o.DB.QueryRowContext(ctx, `SELECT standby_kit FROM cluster_nodes WHERE id = $1`, b).Scan(&kit)
	if calledWith(h, "standby.forget@s-"+b) != 1 || len(kit) != 0 {
		t.Fatalf("the kit kept: %s %v", kit, h.called())
	}
	if calledWith(h, "home.standbys@") != 4 {
		t.Fatalf("the machines were not told there is no standby now: %v", h.called())
	}
}

// sealReport is the old home's seal (the supervisor's switchover.SealReport).
func sealReport(t *testing.T, oldPriv, clusterPub []byte, term int64, plain []byte) string {
	t.Helper()
	salt := fmt.Sprintf("term-%d", term)
	shared, err := curve25519.X25519(oldPriv, clusterPub)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := hkdf.Key(sha256.New, shared, []byte(salt), reportInfo, chacha20poly1305.KeySize)
	aead, _ := chacha20poly1305.New(key)
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, []byte(salt)))
}

func newKey(t *testing.T) (priv, pub []byte) {
	t.Helper()
	priv = make([]byte, 32)
	_, _ = rand.Read(priv)
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

// The old home's report is taken sealed by it to the cluster only: once, from the
// machine the emergency of its term registered it as (not one of another term, nor one
// whose address says another than its body); listed with its counts.
func TestTheOldHomesReportIsTakenSealedOnly(t *testing.T) {
	o, _, _, _, _ := setup(t)
	ctx := context.Background()
	clusterPriv, clusterPub := newKey(t)
	o.ClusterKeyFile = filepath.Join(t.TempDir(), "home-cluster.key")
	if err := os.WriteFile(o.ClusterKeyFile, clusterPriv, 0o600); err != nil {
		t.Fatal(err)
	}
	oldPriv, oldPub := newKey(t)
	old := uuid.NewString()
	// term 3's emergency registered the old home; term 5's came later
	if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_terms (term, home_name, reason, started_at)
	    VALUES (3, 'b', 'emergency', now() - interval '1 minute'), (5, 'c', 'emergency', now() + interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, approved_at,
	        approved_by, full_node) VALUES ($1, 'a', 'approved', 'linux', 'amd64', $2, now(), 'switchover', true)`,
		old, base64.StdEncoding.EncodeToString(oldPub)); err != nil {
		t.Fatal(err)
	}
	report, _ := json.Marshal(map[string]any{"id": uuid.NewString(), "term": 3, "from_node": old, "from_name": "a",
		"cut_lsn": "0/16B3748", "aside": map[string]any{"transactions": []any{
			map[string]any{"commit_lsn": "0/16B4000", "changes": []any{map[string]any{"table": "public.app_settings",
				"kind": "insert"}}}}, "counts": map[string]int{"public.app_settings": 1}}})
	postAs := func(asNode string, asTerm int64, node string, term int64, sealed string) int {
		body, _ := json.Marshal(map[string]any{"node_id": node, "term": term, "sealed": sealed})
		rec := httptest.NewRecorder()
		u := fmt.Sprintf("/api/cluster/kept-aside/report?node=%s&term=%d", asNode, asTerm)
		o.postReport(rec, httptest.NewRequest(http.MethodPost, u, bytes.NewReader(body)))
		return rec.Code
	}
	post := func(node string, term int64, sealed string) int { return postAs(node, term, node, term, sealed) }
	if code := post(old, 3, sealReport(t, oldPriv, clusterPub, 3, report)); code != http.StatusOK {
		t.Fatalf("the old home's report: %d", code)
	}
	if code := post(old, 3, sealReport(t, oldPriv, clusterPub, 3, report)); code != http.StatusOK {
		t.Fatalf("the same report again: %d", code)
	}
	strangerPriv, _ := newKey(t)
	if code := post(old, 3, sealReport(t, strangerPriv, clusterPub, 3, report)); code != http.StatusForbidden {
		t.Fatalf("a report sealed by another key: %d", code)
	}
	if code := post(uuid.NewString(), 3, sealReport(t, oldPriv, clusterPub, 3, report)); code != http.StatusForbidden {
		t.Fatalf("a report of a machine that was never an old home: %d", code)
	}
	if code := post(old, 5, sealReport(t, oldPriv, clusterPub, 5, report)); code != http.StatusForbidden {
		t.Fatalf("a report for a term whose emergency did not register it: %d", code)
	}
	if code := post(old, 4, sealReport(t, oldPriv, clusterPub, 4, report)); code != http.StatusForbidden {
		t.Fatalf("a report for a term that was no emergency: %d", code)
	}
	if code := postAs(old, 3, old, 4, sealReport(t, oldPriv, clusterPub, 4, report)); code != http.StatusBadRequest {
		t.Fatalf("a report whose body says another term than its address: %d", code)
	}
	rs, err := o.Reports(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].FromName != "a" || rs[0].Term != 3 || rs[0].Changes != 1 || rs[0].Txs != 1 ||
		rs[0].State != "reported" || rs[0].Counts["public.app_settings"] != 1 {
		t.Fatalf("the reports: %+v", rs)
	}
}
