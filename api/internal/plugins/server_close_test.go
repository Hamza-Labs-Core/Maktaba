package plugins

import (
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/statements"
)

// Close is safe on a nil server and on one without an executor, and stops the executor
// it has (its reaper ends; a second Close is a no-op) without touching a database.
func TestServerCloseStopsItsExecutor(t *testing.T) {
	var nilServer *Server
	nilServer.Close()
	(&Server{}).Close()

	s := &Server{Statements: statements.New(nil)}
	done := make(chan struct{})
	go func() {
		s.Close()
		s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Server.Close did not return: the executor's reaper never stopped")
	}
	if st := s.Statements.Stats(); st.OpenTransactions != 0 || st.ConnectionsHeld != 0 {
		t.Fatalf("after Close the executor still holds %+v", st)
	}
}
