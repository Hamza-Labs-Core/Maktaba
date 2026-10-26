package auth

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// Epic 16 story 16.2: open registration takes a seat like an admin's
// create; the first user (the home's admin) always may register.
func TestRegistration_TakesASeat(t *testing.T) {
	c := newCloudHome(t)
	store := subscriptions.NewStore()
	c.h.Seats = subscriptions.NewSeatLimiter(store)
	c.h.SeatCount = users.New(c.db)

	reg := func(name string) answer {
		return c.do(http.MethodPost, "/api/auth/register", "", map[string]string{
			"username": name, "email": name + "@example.com", "password": "correct horse battery staple",
		})
	}
	if a := reg("first"); a.code/100 != 2 {
		t.Fatalf("the first user must register on any tier: %d %v", a.code, a.body)
	}
	if _, err := c.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('auth.open_registration', '"true"')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	a := reg("second")
	if a.code != http.StatusForbidden || a.body["type"] != TypeSeatLimit {
		t.Fatalf("a second registration on Free: %d %v", a.code, a.body)
	}
	if d, _ := a.body["detail"].(string); !strings.Contains(d, "Free plan allows 1 user") {
		t.Fatalf("detail not plain: %q", d)
	}
	store.Set(&subscriptions.Entitlements{Tier: subscriptions.TierHome})
	if a := reg("third"); a.code/100 != 2 {
		t.Fatalf("on Home a second user registers: %d %v", a.code, a.body)
	}
}
