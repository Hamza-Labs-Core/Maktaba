package supervisor

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// A busy database (too many clients) is alive: never killed as a hang; one
// recovering is starting; one not answering fails the probe.
func TestPostgresCheck(t *testing.T) {
	t.Parallel()
	if err := postgresCheck(fmt.Errorf("%w: too many clients (53300)", pgbundle.ErrRefused)); err != nil {
		t.Errorf("refused = %v, want alive", err)
	}
	if err := postgresCheck(fmt.Errorf("%w: recovering", pgbundle.ErrStarting)); !errors.Is(err, ErrStarting) {
		t.Errorf("starting = %v, want ErrStarting", err)
	}
	down := errors.New("connection refused")
	if err := postgresCheck(down); !errors.Is(err, down) || errors.Is(err, ErrStarting) {
		t.Errorf("down = %v", err)
	}
	if err := postgresCheck(nil); err != nil {
		t.Errorf("ready = %v", err)
	}
}
