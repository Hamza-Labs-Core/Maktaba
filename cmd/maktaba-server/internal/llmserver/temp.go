package llmserver

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// StartTemp runs the bundled server for a while on a free loopback port, with
// the install's models folder (first-run setup downloads the models before the
// supervisor runs). stop ends it. Its output goes to log.
func StartTemp(ctx context.Context, s Server, log io.Writer, wait time.Duration) (string, func(), error) {
	port, err := FreePort()
	if err != nil {
		return "", nil, err
	}
	s.Port = port
	if err := os.MkdirAll(s.Models, 0o700); err != nil {
		return "", nil, err
	}
	cmd := exec.Command(s.Bin, "serve")
	cmd.Env = append(os.Environ(), s.Env()...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start %s: %w", s.Bin, err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	stop := func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	c := NewClient(s.URL())
	deadline := time.Now().Add(wait)
	for {
		if _, err := c.Version(ctx); err == nil {
			return s.URL(), stop, nil
		}
		select {
		case <-done:
			return "", nil, fmt.Errorf("%s serve exited before it answered (see %s)", s.Bin, s.Logs)
		case <-ctx.Done():
			stop()
			return "", nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			stop()
			return "", nil, fmt.Errorf("%s serve did not answer within %s", s.Bin, wait)
		}
	}
}
