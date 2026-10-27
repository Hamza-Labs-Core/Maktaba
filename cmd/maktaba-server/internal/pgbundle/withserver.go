package pgbundle

import (
	"context"
	"fmt"
	"io"
)

// WithServer runs fn with a server of this cluster up on its loopback port
// (no supervisor runs one yet), then stops it: the desktop app's setup
// creates the database, runs the migrations and seeds the admin this way
// before the server starts for the first time (Epic 38 story 38.6).
func (b Bundle) WithServer(ctx context.Context, log io.Writer, fn func() error) error {
	return b.withServer(ctx, log, fn)
}

// Verify starts a server of this cluster on its loopback port, asks it for its
// databases as the superuser, and stops it: a storage move checks its copy of
// the cluster so before it points the install there (Epic 36 story 36.15).
func (b Bundle) Verify(ctx context.Context, log io.Writer) error {
	return b.withServer(ctx, log, func() error {
		q, err := b.NewQuerier()
		if err != nil {
			return err
		}
		rows, err := q.Query(ctx, "postgres", "SELECT count(*) FROM pg_database")
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return fmt.Errorf("the copy of the database answered %d rows, not 1", len(rows))
		}
		return nil
	})
}
