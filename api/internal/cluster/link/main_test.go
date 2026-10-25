package link

import (
	"os"
	"testing"
	"time"
)

// The tests reconnect in milliseconds, not the field's seconds.
func TestMain(m *testing.M) {
	backoffFloor, backoffCeiling = 20*time.Millisecond, 200*time.Millisecond
	os.Exit(m.Run())
}
