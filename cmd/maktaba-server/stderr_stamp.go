package main

import (
	"bufio"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// stampStderr makes serve's own stderr lines (the supervisor's "[serve]" and "[update]"
// lines, the database step's relayed migrate output, the internal packages' lines) start
// with their time, RFC 3339 UTC, the way the app's agent writes server.log's: the logs page
// and the bundle place them in an update's window (internal/logfiles), where a line with
// no time could only take the one before it. The supervised roles keep the stderr serve
// was given (supervisor.ChildStderr), as do the Postgres programs pgbundle runs
// (pgbundle.ChildOutput): their lines carry their own times. stop puts it back
// once every line written so far is out (it runs before serve returns or exits).
func stampStderr() (stop func()) {
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	supervisor.ChildStderr = orig
	pgbundle.ChildOutput = orig
	os.Stderr = w
	done := make(chan struct{})
	go func() {
		defer close(done)
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if line != "" {
				_, _ = io.WriteString(orig, stampLine(time.Now(), line))
			}
			if err != nil {
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			os.Stderr = orig
			supervisor.ChildStderr = nil
			pgbundle.ChildOutput = nil
			_ = w.Close()
			select {
			case <-done:
			case <-time.After(2 * time.Second): // a writer holding the pipe: its lines are lost, serve is not held
			}
			_ = r.Close()
		})
	}
}

// stampLine is a line of serve's stderr with its time in front.
func stampLine(t time.Time, line string) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z") + " " + line
}
