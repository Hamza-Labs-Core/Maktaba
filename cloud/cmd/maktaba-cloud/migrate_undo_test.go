package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
)

// deploy/cloud-relay/ops/rollout.sh decides by these exit codes: kept, refused, failed.
func TestMigrateUndoExitCodes(t *testing.T) {
	for want, err := range map[int]error{
		3: fmt.Errorf("%w: x", db.ErrUndoKeeps),
		4: fmt.Errorf("%w: x", db.ErrUndoLossy),
		1: errors.New("a Down failed"),
	} {
		if got := undoExit(err); got != want {
			t.Errorf("%v: %d, want %d", err, got, want)
		}
	}
	if got, err := parseVersions([]string{"500025", "40002"}); err != nil || len(got) != 2 || got[0] != 500025 || got[1] != 40002 {
		t.Errorf("%v %v", got, err)
	}
	for _, bad := range [][]string{nil, {"x"}, {"0"}, {"-1"}, {"1", ""}} {
		if _, err := parseVersions(bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}
