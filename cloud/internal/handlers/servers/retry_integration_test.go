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
// same home under the slug it has by now, the same secret), and an agent that sends none (an older
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
	MountAgent(r, Deps{Servers: srvs, ClaimKey: []byte("the cloud's own key")})
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
	if st != http.StatusCreated || again["server_id"] != first["server_id"] || again["slug"] != "renamed-box" || again["server_secret"] != first["server_secret"] {
		t.Fatalf("with the key: %d %v, first %v", st, again, first)
	}
}

// The cloud's own key changed between the first answer and the second, or two of its nodes have
// different ones: the claim is refused rather than answered with another secret, so every answer
// given works, two at once included. Without a key of its own the cloud takes no retry key at all.
func TestRedeemRetryAcrossTheCloudsKeys(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	u, err := stores.NewUsers(pool.DB).Create(ctx, "owner@example.test", "x", "Owner", true)
	if err != nil {
		t.Fatal(err)
	}
	srvs := stores.NewServers(pool.DB)
	redeem := func(claimKey, code string) (int, map[string]any) {
		t.Helper()
		r := chi.NewRouter()
		MountAgent(r, Deps{Servers: srvs, ClaimKey: []byte(claimKey)})
		b, _ := json.Marshal(map[string]any{"code": code, "name": "box", "slug": "key-" + strings.ToLower(code), "retry_key": strings.Repeat("0f", 32)})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/servers/claims/redeem", bytes.NewReader(b)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	mint := func() string {
		t.Helper()
		code, err := srvs.MintClaim(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return code
	}
	code := mint()
	_, first := redeem("one", code)
	if st, again := redeem("two", code); st != http.StatusNotFound || again["error"] != "claim_invalid" {
		t.Fatalf("under another key: %d %v", st, again)
	}
	if st, again := redeem("one", code); st != http.StatusCreated || again["server_secret"] != first["server_secret"] {
		t.Fatalf("under its own key again: %d %v", st, again)
	}
	if err := srvs.VerifySecret(ctx, first["server_id"].(string), first["server_secret"].(string)); err != nil {
		t.Fatalf("the first answer's secret: %v", err)
	}

	// two nodes with different keys at once, several rounds: whichever answers work
	for range 5 {
		code := mint()
		type answer struct {
			st  int
			out map[string]any
		}
		done := make(chan answer, 2)
		for _, k := range []string{"one", "two"} {
			go func() {
				st, out := redeem(k, code)
				done <- answer{st, out}
			}()
		}
		given := 0
		for range 2 {
			a := <-done
			switch a.st {
			case http.StatusCreated:
				given++
				if err := srvs.VerifySecret(ctx, a.out["server_id"].(string), a.out["server_secret"].(string)); err != nil {
					t.Fatalf("an answer's secret does not work: %v", err)
				}
			case http.StatusNotFound:
			default:
				t.Fatalf("%d %v", a.st, a.out)
			}
		}
		if given != 1 {
			t.Fatalf("%d answers of two", given)
		}
	}

	code = mint()
	if st, out := redeem("", code); st != http.StatusCreated {
		t.Fatalf("%d %v", st, out)
	}
	if st, out := redeem("", code); st != http.StatusNotFound {
		t.Fatalf("redeemed again by a cloud with no key: %d %v", st, out)
	}
}
