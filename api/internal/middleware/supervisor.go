package middleware

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
)

// LocalTokenHeader carries the supervisor's per-boot local token
// (MAKTABA_SUPERVISOR_TOKEN_FILE): the requests this machine's own supervisor makes of
// its API (the update watch's /api/cluster/self, the update's TV change).
const LocalTokenHeader = "X-Maktaba-Local-Token"

// FromSupervisor reports whether a request is this machine's own supervisor's: it
// carries the per-boot token the supervisor wrote for its children.
func FromSupervisor(r *http.Request) bool {
	got, file := r.Header.Get(LocalTokenHeader), strings.TrimSpace(os.Getenv("MAKTABA_SUPERVISOR_TOKEN_FILE"))
	if got == "" || file == "" {
		return false
	}
	b, err := os.ReadFile(file)
	want := strings.TrimSpace(string(b))
	return err == nil && want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
