package pgbundle

import (
	"context"
	"io"
)

// FollowReach is one look of WatchReach, for the tests outside the package (they run a
// migrated home through pgtest, which imports this package).
func FollowReach(ctx context.Context, b Bundle, restart func() error, log io.Writer) error {
	return b.followReach(ctx, restart, log)
}
