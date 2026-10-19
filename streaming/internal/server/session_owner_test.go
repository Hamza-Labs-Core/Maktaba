package server

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
)

// A manifest or segment URL signed for one user's session must not open
// another user's session: the resolver 404s unless usr is the owner.
func TestSessionOwnedBy(t *testing.T) {
	owner := uuid.New()
	other := uuid.New()
	cases := []struct {
		name  string
		owner uuid.UUID
		cl    *auth.Claims
		want  bool
	}{
		{"owner", owner, &auth.Claims{Usr: owner.String()}, true},
		{"another user", owner, &auth.Claims{Usr: other.String()}, false},
		{"malformed usr", owner, &auth.Claims{Usr: "not-a-uuid"}, false},
		{"admin token", owner, &auth.Claims{Usr: other.String(), IsAdmin: true}, true},
		{"no usr (older signer)", owner, &auth.Claims{}, true},
		{"session without owner", uuid.Nil, &auth.Claims{Usr: other.String()}, true},
	}
	for _, c := range cases {
		if got := sessionOwnedBy(c.owner, c.cl); got != c.want {
			t.Errorf("%s: sessionOwnedBy = %v, want %v", c.name, got, c.want)
		}
	}
}
