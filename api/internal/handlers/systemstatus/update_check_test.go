package systemstatus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
)

// The update check on the System health page: nothing before the first check, fine while
// it works, marked after one failure, degraded from updates.FailingAfter in a row with
// since when and why (the incident of 2026-11-24: a day of failed checks only logged).
func TestUpdateCheckItem(t *testing.T) {
	ctx := context.Background()
	state := func(st updates.State) *Handler {
		return &Handler{UpdateState: func(context.Context) (updates.State, bool) { return st, true }}
	}
	if items := checkUpdateCheck(ctx, newEnv(state(updates.State{}))); len(items) != 0 {
		t.Fatalf("an item before any check: %+v", items)
	}
	at := t0.Add(-time.Hour)
	it := only(t, checkUpdateCheck(ctx, newEnv(state(updates.State{LastCheckAt: &at, LastCheckOK: &at}))))
	want(t, it, StatusOK, "update_check_ok")
	if it.Link != "/admin/updates" || it.Group != GroupIntegrations || it.Kind != KindUpdateCheck {
		t.Fatalf("item %+v", it)
	}

	since := t0.Add(-26 * time.Hour)
	msg := "GET https://api.github.com/repos/o/r/releases?per_page=30: larger than 8388608 bytes"
	once := updates.State{LastCheckAt: &at, Failures: 1, FailingSince: &since, LastCheckCode: updates.CheckFailTooLarge, LastCheckErr: msg}
	it = only(t, checkUpdateCheck(ctx, newEnv(state(once))))
	want(t, it, StatusOK, "update_check_failed_once")
	if !it.Attention {
		t.Fatal("one failure is not marked")
	}

	failing := once
	failing.Failures = updates.FailingAfter
	it = only(t, checkUpdateCheck(ctx, newEnv(state(failing))))
	want(t, it, StatusDegraded, "update_check_failing")
	if it.Params["since"] != since.Format(time.RFC3339) || it.Params["code"] != updates.CheckFailTooLarge ||
		it.Params["failing_seconds"] != int64(26*3600) || !strings.Contains(it.Reason, "larger than 8388608 bytes") ||
		!strings.HasPrefix(it.Reason, "Can't check for updates since ") {
		t.Fatalf("item %+v", it)
	}
	// the overall state says so
	if st, _, _, _ := rollup([]Item{it}); st != StatusDegraded {
		t.Fatalf("rollup %s", st)
	}
}
