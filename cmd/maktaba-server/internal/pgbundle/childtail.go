package pgbundle

import (
	"strings"
	"sync"
)

// startTailBytes is how much of a server's own output an "exited while starting" error
// carries: its last lines, where Postgres's FATAL says why.
const startTailBytes = 4096

// tailWriter keeps the last max bytes written to it.
type tailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
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
