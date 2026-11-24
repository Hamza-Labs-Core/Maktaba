//go:build integration

package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// The redeem route hands the agent's retry_key on: the same key redeems a spent code again (the
// same home under the slug it has by now, a new secret), and an agent that sends none (an older
// one) cannot. The store's rules are in cmd/maktaba-cloud's TestRedeemAgainAfterALostAnswer.
func TestRedeemRouteTakesTheRetryKey(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	u, err := stores.NewUsers(pool.DB).Create(ctx, "owner@example.test", "x", "Owner", true)
	if err != nil {
		t.Fatal(err)
	}
	srvs := stores.NewServers(pool.DB)
	r := chi.NewRouter()
	MountAgent(r, Deps{Servers: srvs})
	redeem := func(body map[string]any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/servers/claims/redeem", bytes.NewReader(b)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, err := srvs.MintClaim(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("0f", 32)
	st, first := redeem(map[string]any{"code": code, "name": "box", "slug": "retry-box", "retry_key": key})
	if st != http.StatusCreated {
		t.Fatalf("%d %v", st, first)
	}
	if _, err := pool.DB.Exec(`UPDATE servers SET slug = 'renamed-box' WHERE id = $1`, first["server_id"]); err != nil {
		t.Fatal(err)
	}
	if st, out := redeem(map[string]any{"code": code, "name": "box", "slug": "retry-box"}); st != http.StatusNotFound || out["error"] != "claim_invalid" {
		t.Fatalf("without the key: %d %v", st, out)
	}
	st, again := redeem(map[string]any{"code": code, "name": "box", "slug": "retry-box", "retry_key": key})
	if st != http.StatusCreated || again["server_id"] != first["server_id"] || again["slug"] != "renamed-box" || again["server_secret"] == first["server_secret"] {
		t.Fatalf("with the key: %d %v, first %v", st, again, first)
	}
}
