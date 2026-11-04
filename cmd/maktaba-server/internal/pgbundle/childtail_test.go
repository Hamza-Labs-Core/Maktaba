package pgbundle

import (
	"errors"
	"strings"
	"testing"
)

// A server's own output is kept to its last lines: a line the limit cut goes, the
// FATAL at the end stays.
func TestTailWriterKeepsTheLastLines(t *testing.T) {
	var log strings.Builder
	w := &tailWriter{max: 64, next: &log}
	if w.said() != "" {
		t.Fatalf("said %q before any output", w.said())
	}
	for i := 0; i < 20; i++ {
		_, _ = w.Write([]byte("LOG:  a line of the start\n"))
	}
	_, _ = w.Write([]byte("FATAL:  could not bind\n"))
	got := w.said()
	if !strings.HasSuffix(got, "FATAL:  could not bind") || !strings.HasPrefix(got, "; postgres said:\nLOG:  a line") {
		t.Fatalf("said %q", got)
	}
	if len(got) > 64+len("; postgres said:\n") {
		t.Fatalf("said %d bytes, more than the limit", len(got))
	}
	if !strings.HasSuffix(log.String(), "FATAL:  could not bind\n") || len(log.String()) < 20*26 {
		t.Fatalf("the log got %d bytes, not everything", log.Len())
	}
}

type brokenLog struct{}

func (brokenLog) Write([]byte) (int, error) { return 0, errors.New("closed") }

// A log that fails never stops the copy: the program would block on a full pipe.
func TestTailWriterOutlivesItsLog(t *testing.T) {
	w := &tailWriter{max: 64, next: brokenLog{}}
	if n, err := w.Write([]byte("FATAL:  x\n")); n != 10 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if !strings.HasSuffix(w.said(), "FATAL:  x") {
		t.Fatalf("said %q", w.said())
	}
}
