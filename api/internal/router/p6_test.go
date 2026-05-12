package router

import (
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
)

// TestMountP6_NilDBIsNoop guards against a startup crash when
// DATABASE_URL is unset. Production wires a real *sql.DB; this test
// proves the wiring is safe to call without one.
func TestMountP6_NilDBIsNoop(_ *testing.T) {
	r := New(Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	MountP6(r, P6Deps{DB: nil})
	// No panic = pass.
	_ = httptest.NewServer(r)
}
