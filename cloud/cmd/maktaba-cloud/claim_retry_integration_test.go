//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
)

// A redeem whose answer was lost: the same agent (the same retry key) redeems the code again
// within the claim's lifetime and gets the same home with a new secret; nobody else can, and
// nobody can once the home connected, the claim expired, the owner was suspended or the home
// was deleted.
func TestRedeemAgainAfterALostAnswer(t *testing.T) {
	key, other := strings.Repeat("a1", 32), strings.Repeat("b2", 32)
	type world struct {
		e    *env
		code string
		uid  string
	}
	setup := func(t *testing.T) world {
		e := newEnv(t)
		owner := e.registerVerified("retry@example.test")
		code := owner.mustStatus(owner.do("POST", "/v1/servers/claims", nil), http.StatusOK).json(t)["code"].(string)
		return world{e, code, e.userID("retry@example.test")}
	}
	redeem := func(w world, slug, retry string) resp {
		return w.e.client().do("POST", "/v1/servers/claims/redeem", map[string]string{"code": w.code, "name": "H", "slug": slug, "retry_key": retry})
	}
	first := func(t *testing.T, w world, retry string) map[string]any {
		t.Helper()
		r := redeem(w, "retry-home", retry)
		if r.code != http.StatusCreated {
			t.Fatalf("redeem: %d %s", r.code, r.body)
		}
		return r.json(t)
	}
	refused := func(t *testing.T, r resp) {
		t.Helper()
		if r.code != http.StatusNotFound || r.errCode() != "claim_invalid" {
			t.Fatalf("%d %s, want 404 claim_invalid", r.code, r.body)
		}
	}

	t.Run("the same key", func(t *testing.T) {
		w := setup(t)
		a := first(t, w, key)
		// the slug asked for the second time does not matter: the home is the one made
		r := redeem(w, "another-slug", key)
		if r.code != http.StatusCreated {
			t.Fatalf("again: %d %s", r.code, r.body)
		}
		b := r.json(t)
		if b["server_id"] != a["server_id"] || b["slug"] != "retry-home" || b["server_secret"] == a["server_secret"] || b["assertion_public_key"] != a["assertion_public_key"] {
			t.Fatalf("again: %v, first %v", b, a)
		}
		if n := w.e.count(`SELECT count(*) FROM servers WHERE owner_user_id = $1`, w.uid); n != 1 {
			t.Fatalf("%d homes", n)
		}
		var hash string
		if err := w.e.pool.DB.QueryRow(`SELECT server_secret_hash FROM servers WHERE id = $1`, a["server_id"]).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if argon2id.Verify(b["server_secret"].(string), hash) != nil {
			t.Fatal("the new secret does not work")
		}
		if argon2id.Verify(a["server_secret"].(string), hash) == nil {
			t.Fatal("the lost answer's secret still works")
		}
		if n := w.e.count(`SELECT count(*) FROM server_claims WHERE retry_key_hash LIKE '%' || $1 || '%'`, key); n != 0 {
			t.Fatal("the retry key is stored as it is")
		}
	})
	t.Run("another key, none, a short one", func(t *testing.T) {
		w := setup(t)
		first(t, w, key)
		for _, k := range []string{other, "", "a1"} {
			refused(t, redeem(w, "retry-home", k))
		}
	})
	t.Run("redeemed without a key", func(t *testing.T) {
		w := setup(t)
		first(t, w, "")
		refused(t, redeem(w, "retry-home", key))
		refused(t, redeem(w, "retry-home", ""))
	})
	t.Run("a key too short to be one", func(t *testing.T) {
		w := setup(t)
		first(t, w, "a1")
		refused(t, redeem(w, "retry-home", "a1"))
	})
	for name, sql := range map[string]string{
		"the home connected": `UPDATE servers SET last_seen_at = now() WHERE owner_user_id = $1`,
		"the claim expired":  `UPDATE server_claims SET expires_at = now() - INTERVAL '1 second' WHERE user_id = $1`,
		"owner suspended":    `UPDATE users SET status = 'suspended', suspended_at = now() WHERE id = $1`,
		"the home deleted":   `DELETE FROM servers WHERE owner_user_id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			w := setup(t)
			first(t, w, key)
			w.e.exec(sql, w.uid)
			refused(t, redeem(w, "retry-home", key))
		})
	}
	t.Run("a taken slug first", func(t *testing.T) {
		w := setup(t)
		w.e.exec(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, 'X', 'taken-home', 'x')`, w.uid)
		if r := redeem(w, "taken-home", key); r.code != http.StatusConflict {
			t.Fatalf("taken: %d %s", r.code, r.body)
		}
		// the refusal spent nothing: another agent may still use the code
		if r := redeem(w, "free-home", other); r.code != http.StatusCreated {
			t.Fatalf("after a taken slug: %d %s", r.code, r.body)
		}
	})
	t.Run("two at once with the same key", func(t *testing.T) {
		w := setup(t)
		done := make(chan resp, 2)
		for range 2 {
			c := w.e.client()
			go func() {
				done <- c.do("POST", "/v1/servers/claims/redeem", map[string]string{"code": w.code, "name": "H", "slug": "retry-home", "retry_key": key})
			}()
		}
		ids := map[any]bool{}
		for range 2 {
			r := <-done
			if r.code != http.StatusCreated {
				t.Fatalf("%d %s", r.code, r.body)
			}
			ids[r.json(t)["server_id"]] = true
		}
		if len(ids) != 1 || w.e.count(`SELECT count(*) FROM servers WHERE owner_user_id = $1`, w.uid) != 1 {
			t.Fatalf("homes: %v", ids)
		}
	})
}
