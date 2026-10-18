package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// TestPGJoinApproveRevoke drives the whole join through the handlers on a
// real Postgres: the machine decrypts its credentials, logs in with its own
// role, and cannot once revoked. Runs only with MAKTABA_TEST_PG_URL.
func TestPGJoinApproveRevoke(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	base, _ := url.Parse(os.Getenv("MAKTABA_TEST_PG_URL"))
	port, _ := strconv.Atoi(base.Port())
	if port == 0 {
		port = 5432
	}
	h := &Handler{
		Store: &cluster.PGStore{DB: db}, Roles: &cluster.PGRoles{DB: db}, PGPort: port,
		JoinRatePerMin: -1, PollRatePerMin: -1,
	}
	r := chi.NewRouter()
	h.Mount(r)
	ctx := context.Background()
	if _, err := h.Bootstrap(ctx, "home-test"); err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string, p *principal.Principal) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = base.Hostname() + ":8080"
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	var home Home
	decode(t, do("PUT", "/api/cluster/settings", `{"accept_joins":true}`, admin()), 200, &home)
	if !home.RolesSupported {
		t.Fatalf("roles unsupported: %v", *home.RolesReason)
	}

	nodePriv, nodePub, _ := cluster.GenerateKey()
	id := uuid.NewString()
	role := cluster.RoleName(id)
	t.Cleanup(func() { _ = (&cluster.PGRoles{DB: db}).Drop(context.Background(), role) })
	var jr JoinResponse
	decode(t, do("POST", "/api/cluster/join", joinBody(id, cluster.EncodeKey(nodePub)), nil), 202, &jr)

	homePub, _ := cluster.DecodeKey(jr.HomePubkey)
	shared, _ := cluster.SharedSecret(nodePriv, homePub)
	code, _ := cluster.SAS(shared, nodePub, homePub)
	decode(t, do("POST", "/api/cluster/nodes/"+id+"/approve", `{"code":"`+code+`"}`, admin()), 200, nil)

	var pr PollResponse
	decode(t, do("GET", "/api/cluster/join/"+id+"?pubkey="+url.QueryEscape(cluster.EncodeKey(nodePub)), "", nil), 200, &pr)
	if pr.State != "approved" || pr.Sealed == nil {
		t.Fatalf("poll = %+v", pr)
	}
	key, _ := cluster.SealKey(shared, nodePub, homePub)
	plain, err := cluster.Open(key, id, *pr.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	var p cluster.Payload
	if err := json.Unmarshal(plain, &p); err != nil {
		t.Fatal(err)
	}
	// lib/pq has no sslmode=prefer; the Python node does.
	nodeDSN := strings.Replace(p.DatabaseURL, "sslmode=prefer", "sslmode=disable", 1)
	nodeDB, err := sql.Open("postgres", nodeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer nodeDB.Close()
	var state string
	if err := nodeDB.QueryRow(`SELECT state FROM cluster_node_beat($1)`, id).Scan(&state); err != nil || state != "approved" {
		t.Fatalf("node beat as its own role: %q %v", state, err)
	}
	var other int
	if err := nodeDB.QueryRow(`SELECT count(*) FROM cluster_node_beat('00000000-0000-0000-0000-000000000000')`).Scan(&other); err == nil {
		t.Error("a node must not beat for another machine")
	}

	decode(t, do("POST", "/api/cluster/nodes/"+id+"/revoke", "", admin()), 200, nil)
	fresh, _ := sql.Open("postgres", nodeDSN)
	defer fresh.Close()
	if err := fresh.PingContext(ctx); err == nil {
		t.Error("revoked role still logs in")
	}
	decode(t, do("GET", "/api/cluster/join/"+id+"?pubkey="+url.QueryEscape(cluster.EncodeKey(nodePub)), "", nil), 200, &pr)
	if pr.State != "revoked" {
		t.Errorf("poll after revoke = %q", pr.State)
	}
	if rec := do("DELETE", "/api/cluster/nodes/"+id, "", admin()); rec.Code != http.StatusNoContent {
		t.Errorf("delete = %d %s", rec.Code, rec.Body.String())
	}
}
