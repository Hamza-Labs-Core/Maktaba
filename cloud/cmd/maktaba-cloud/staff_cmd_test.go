package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// T12 (story 50.33), the checks before the database: an unknown command, a missing email or
// a role that does not exist are refused, and the old admin command points to staff.
func TestStaffCommandRefusals(t *testing.T) {
	var out bytes.Buffer
	c := staffCmd{Out: &out, Now: time.Now}
	for _, tc := range []struct{ sub, email, role, want string }{
		{"promote", "a@example.test", "service_admin", "unknown command"},
		{"grant", "", "service_admin", "--email is required"},
		{"add", "a@example.test", "moderator", "--role must be one of: service_admin"},
		{"revoke", "a@example.test", "", "--role must be one of"},
	} {
		err := c.run(context.Background(), tc.sub, tc.email, tc.role)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("staff %s --email %q --role %q: %v, want %q", tc.sub, tc.email, tc.role, err, tc.want)
		}
	}
	out.Reset()
	printAdminPointer(&out)
	if !strings.Contains(out.String(), "maktaba-cloud staff add --email") {
		t.Fatalf("pointer: %q", out.String())
	}
}
