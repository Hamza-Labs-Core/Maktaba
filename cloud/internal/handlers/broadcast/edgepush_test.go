package broadcast

import (
	"context"
	"testing"
)

type countingEdge struct{ posts int }

func (e *countingEdge) Post(context.Context, string, string, string) (int, []byte, error) {
	e.posts++
	return 204, nil, nil
}

// PushAccess is what a plan change elsewhere (the billing webhook, the operator console) calls
// after its commit. A cloud with no broadcast key or no edge has nothing to tell: it posts
// nothing and reads nothing (a nil DB here would panic), so a plan change there never fails.
// With both set, the push and its retry are pushOne's and PushPending's, covered on the api
// role's router (TestGuestSharingEndsWithThePlan).
func TestPushAccessWithoutAKeyOrEdgeDoesNothing(t *testing.T) {
	edge := &countingEdge{}
	(&Deps{Edge: edge}).PushAccess(context.Background(), "home-1")
	if edge.posts != 0 {
		t.Fatalf("no key: %d posts", edge.posts)
	}
	k := revKey(t, "push")
	(&Deps{Key: &k}).PushAccess(context.Background(), "home-1")
	(&Deps{}).PushAccess(context.Background(), "home-1")
}
