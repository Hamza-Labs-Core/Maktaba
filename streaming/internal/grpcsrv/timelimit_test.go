package grpcsrv

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/timelimit"
)

// The API's push of a time limit and its question about served time, over the wire
// the API uses. A push that cannot be kept is answered with an error.
func TestServe_TimeLimit(t *testing.T) {
	srv, _, _ := setupServer(t)
	conn := dialServer(t, srv)
	user, lib, sess := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now()
	push := map[string]any{
		"user_id": user, "library_id": lib, "reason": timelimit.ReasonOutsideSlot,
		"end_at":     now.Add(time.Hour).Format(time.RFC3339Nano),
		"session_id": sess, "lease_until": now.Add(time.Minute).Format(time.RFC3339Nano),
	}
	if out := invoke(t, conn, "SetTimeLimit", push); out["error"] == nil {
		t.Fatal("a server that keeps no time limits accepted one")
	}
	if out := invoke(t, conn, "TimeServed", map[string]any{"user_id": user, "session_id": sess}); out["error"] == nil {
		t.Fatal("a server that keeps no time limits answered for served time")
	}

	srv.TimeLimits = timelimit.New()
	if out := invoke(t, conn, "SetTimeLimit", push); out["ok"] != true {
		t.Fatalf("SetTimeLimit: %v", out)
	}
	if _, refused := srv.TimeLimits.Check(user, []string{lib}, sess); refused != nil {
		t.Fatalf("the pushed lease is not held: %+v", refused)
	}
	out := invoke(t, conn, "TimeServed", map[string]any{"user_id": user, "session_id": sess})
	if out["known"] != true || out["served_at"] == nil {
		t.Fatalf("TimeServed: %v", out)
	}
	if out := invoke(t, conn, "TimeServed", map[string]any{"user_id": user, "session_id": uuid.NewString()}); out["known"] != false {
		t.Fatalf("TimeServed for a session with no lease: %v", out)
	}
	for name, bad := range map[string]map[string]any{
		"no user":                {"library_id": lib},
		"a time it cannot read":  {"user_id": user, "library_id": lib, "end_at": "soon"},
		"a lease for no session": {"user_id": user, "library_id": lib, "lease_until": now.Format(time.RFC3339Nano)},
	} {
		if out := invoke(t, conn, "SetTimeLimit", bad); out["error"] == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// the end pushed as past refuses at once; cleared, the account has no end
	push["end_at"] = now.Add(-time.Second).Format(time.RFC3339Nano)
	invoke(t, conn, "SetTimeLimit", push)
	if _, refused := srv.TimeLimits.Check(user, []string{lib}, sess); refused == nil || refused.Reason != timelimit.ReasonOutsideSlot {
		t.Fatalf("after a past end: %+v", refused)
	}
	invoke(t, conn, "SetTimeLimit", map[string]any{"user_id": user, "library_id": lib, "clear": true})
	if _, refused := srv.TimeLimits.Check(user, []string{lib}, ""); refused != nil {
		t.Fatalf("after the limit was cleared: %+v", refused)
	}

	// the account is made restricted: a URL that names no session is refused in a
	// library nothing else is held for; lifted, everything held for it goes
	other := uuid.NewString()
	if out := invoke(t, conn, "SetTimeKept", map[string]any{"user_id": user}); out["error"] == nil {
		t.Fatal("SetTimeKept without kept was accepted")
	}
	if out := invoke(t, conn, "SetTimeKept", map[string]any{"kept": true}); out["error"] == nil {
		t.Fatal("SetTimeKept without a user was accepted")
	}
	if out := invoke(t, conn, "SetTimeKept", map[string]any{"user_id": user, "kept": true}); out["ok"] != true {
		t.Fatalf("SetTimeKept: %v", out)
	}
	if _, refused := srv.TimeLimits.Check(user, []string{other}, ""); refused == nil {
		t.Fatal("a kept account was served a URL that names no session")
	}
	invoke(t, conn, "SetTimeLimit", push) // a past end in lib
	if out := invoke(t, conn, "SetTimeLimit", map[string]any{"user_id": user, "clear_all": true}); out["ok"] != true {
		t.Fatalf("SetTimeLimit clear_all: %v", out)
	}
	if _, refused := srv.TimeLimits.Check(user, []string{lib}, ""); refused == nil || refused.Reason != timelimit.ReasonNoHeartbeat {
		t.Fatalf("after clear_all the account is still kept, with no end: %+v", refused)
	}
	invoke(t, conn, "SetTimeLimit", push)
	if out := invoke(t, conn, "SetTimeKept", map[string]any{"user_id": user, "kept": false}); out["ok"] != true {
		t.Fatalf("SetTimeKept false: %v", out)
	}
	for _, l := range []string{lib, other} {
		if _, refused := srv.TimeLimits.Check(user, []string{l}, ""); refused != nil {
			t.Fatalf("after the lift: %+v", refused)
		}
	}
}
