package switchover

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
)

// A machine ended a moment before reconnects between StopServing's look and its drop: the
// drop is refused as in use (55006), which is tried again, not the switchover's failure
// (TestAPlannedSwitchover on CI).
func TestASlotBackInUseIsTriedAgain(t *testing.T) {
	inUse := fmt.Errorf("drop: %w", &pq.Error{Code: "55006", Message: `replication slot "maktaba_copy_x" is active for PID 1`})
	if !slotInUse(inUse) {
		t.Fatal("a slot in use again must be tried again")
	}
	for _, err := range []error{&pq.Error{Code: "42704"}, errors.New("connection refused")} {
		if slotInUse(err) {
			t.Fatalf("%v is not a slot in use", err)
		}
	}
}
