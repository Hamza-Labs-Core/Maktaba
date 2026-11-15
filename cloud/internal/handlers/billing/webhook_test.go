package billing

import (
	"context"
	"slices"
	"testing"
)

// A home both setBroadcast and SetEffectivePlan changed in one event is pushed to the edge
// once; the order of first appearance is kept.
func TestPushAccessPushesEachHomeOnce(t *testing.T) {
	var pushed []string
	d := &Deps{AccessPush: func(_ context.Context, s string) { pushed = append(pushed, s) }}
	d.pushAccess([]string{"h1", "h2", "h1", "h3", "h2"})(context.Background())
	if want := []string{"h1", "h2", "h3"}; !slices.Equal(pushed, want) {
		t.Fatalf("pushed %v, want %v", pushed, want)
	}
	// no push function: nothing to do, and no panic
	(&Deps{}).pushAccess([]string{"h1"})(context.Background())
}
