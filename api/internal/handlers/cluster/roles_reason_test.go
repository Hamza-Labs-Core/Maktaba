package cluster

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// Each reason the home cannot take machines has a code (the apps' words), beside its English.
func TestRolesReasonHasItsCode(t *testing.T) {
	for reason, want := range map[string]string{
		cluster.ReasonSQLite:       "sqlite",
		cluster.ReasonCannotCreate: "cannot_create_roles",
		cluster.ReasonNoGroup:      "no_worker_group",
		"something new":            "",
	} {
		if got, p := rolesReasonCode(reason); got != want || p != nil {
			t.Errorf("%q: %q %v, want %q", reason, got, p, want)
		}
	}
	code, p := rolesReasonCode(cluster.ReasonCheckFailed + "connection refused")
	if code != "check_failed" || p["error"] != "connection refused" {
		t.Errorf("check failed: %q %v", code, p)
	}
}
