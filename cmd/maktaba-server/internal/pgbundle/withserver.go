package pgbundle

import (
	"context"
	"io"
)

// WithServer runs fn with a server of this cluster up on its loopback port
// (no supervisor runs one yet), then stops it: the desktop app's setup
// creates the database, runs the migrations and seeds the admin this way
// before the server starts for the first time (Epic 38 story 38.6).
func (b Bundle) WithServer(ctx context.Context, log io.Writer, fn func() error) error {
	return b.withServer(ctx, log, fn)
}
