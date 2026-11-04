package pgbundle

import (
	"context"
	"io"
	"testing"
	"time"
)

// T10 (Epic 51 story 51.04): the backups component turned off mid-dump: the dump runs on
// with the server's context to its end, then the loop returns.
func TestBackupLoopStopKeepsDump(t *testing.T) {
	loop, stop := context.WithCancel(context.Background())
	work, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	inDump := make(chan struct{})
	finish := make(chan struct{})
	dumpErr := make(chan error, 1)
	dump := func(ctx context.Context, _ time.Time) (string, error) {
		close(inDump)
		<-finish
		dumpErr <- ctx.Err()
		return "dump.sql", nil
	}
	returned := make(chan struct{})
	go func() {
		runBackups(loop, work, time.Millisecond, io.Discard, func() bool { return true }, dump)
		close(returned)
	}()
	<-inDump
	stop()
	select {
	case <-returned:
		t.Fatal("the loop returned during its dump")
	case <-time.After(50 * time.Millisecond):
	}
	close(finish)
	if err := <-dumpErr; err != nil {
		t.Fatalf("the dump's context was cut: %v", err)
	}
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not return after the dump")
	}
}

// RunBackups keeps its old meaning: one context stops both.
func TestBackupLoopNotDue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	runBackups(ctx, ctx, time.Millisecond, io.Discard, func() bool { return false },
		func(context.Context, time.Time) (string, error) {
			t.Error("dumped when not due")
			return "", nil
		})
}
