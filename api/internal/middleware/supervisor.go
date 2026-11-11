package middleware

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// LocalTokenHeader carries the supervisor's per-boot local token
// (MAKTABA_SUPERVISOR_TOKEN_FILE): the requests this machine's own supervisor makes of
// its API (the update watch's /api/cluster/self, the update's TV change).
const LocalTokenHeader = "X-Maktaba-Local-Token"

// localTokenTTL is how long a read of the token file stands: InternalCallers checks the
// header before any client limit, so a client sending one must not cost a file read per
// request. The token changes only when the supervisor starts again.
const localTokenTTL = time.Second

var localTokenCache struct {
	mu   sync.Mutex
	file string
	tok  string
	at   time.Time
}

// localTokenFrom answers the token in file (trimmed; "" when it cannot be read), read at
// most once a localTokenTTL per file.
func localTokenFrom(file string) string {
	c := &localTokenCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file == file && time.Since(c.at) < localTokenTTL {
		return c.tok
	}
	b, err := os.ReadFile(file) //nolint:gosec // this machine's own token file
	c.file, c.at, c.tok = file, time.Now(), ""
	if err == nil {
		c.tok = strings.TrimSpace(string(b))
	}
	return c.tok
}

// FromSupervisor reports whether a request is this machine's own supervisor's: it
// carries the per-boot token the supervisor wrote for its children.
func FromSupervisor(r *http.Request) bool {
	got, file := r.Header.Get(LocalTokenHeader), strings.TrimSpace(os.Getenv("MAKTABA_SUPERVISOR_TOKEN_FILE"))
	if got == "" || file == "" {
		return false
	}
	want := localTokenFrom(file)
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
