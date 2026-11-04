package pgbundle

import (
	"io"
	"strings"
	"sync"
)

// startTailBytes is how much of a server's own output an "exited while starting" error
// carries: its last lines, where Postgres's FATAL says why.
const startTailBytes = 4096

// tailWriter passes what a program writes on to next and keeps the last max bytes of
// it. A failed write to next is not the program's: it never stops the copy (a stopped
// copy would leave the program blocked on a full pipe).
type tailWriter struct {
	mu   sync.Mutex
	max  int
	next io.Writer
	buf  []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.next != nil {
		_, _ = w.next.Write(p)
	}
	w.buf = append(w.buf, p...)
	if over := len(w.buf) - w.max; over > 0 {
		w.buf = append(w.buf[:0], w.buf[over:]...)
	}
	return len(p), nil
}

// said is what the program wrote, for an error: "" when nothing, else its last lines
// (a first line cut by the limit dropped) after a newline.
func (w *tailWriter) said() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	text := string(w.buf)
	if len(w.buf) == w.max {
		if _, rest, ok := strings.Cut(text, "\n"); ok {
			text = rest
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return "; postgres said:\n" + text
}
