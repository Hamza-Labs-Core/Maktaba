package pgbundle

import (
	"strings"
	"testing"
)

// A server's own output is kept to its last lines: a line the limit cut goes, the
// FATAL at the end stays.
func TestTailWriterKeepsTheLastLines(t *testing.T) {
	w := &tailWriter{max: 64}
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
}
