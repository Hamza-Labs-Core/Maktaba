package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// lockedBuffer is a log's output read while the relay may still write to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Epic 53 story 53.17 T01: a relay request that failed is logged on the 53.01 schema with
// service cloud.relay, its node, the request's id and the home as home_h: never its slug,
// its server id, an email or a token.
func TestARelayFailureNamesTheHomeByHomeHAndCarriesTheRequestID(t *testing.T) {
	key := bytes.Repeat([]byte{7}, mlog.LogKeySize)
	mlog.SetUserHashKey(key)
	t.Cleanup(func() { mlog.SetUserHashKey(nil) })
	api := simhome.API()
	defer api.Close()
	dir, store, lookup := relaypkg.NewMemDirectory(), simhome.NewStore(), &simLookup{}
	blue := newProc(t, "a-blue", dir, store, lookup, 300*time.Millisecond, nil)
	green := newProc(t, "a-green", dir, store, lookup, 300*time.Millisecond, nil)
	var out lockedBuffer
	green.deps.Logger = mlog.NewLogger(mlog.Options{Service: "cloud.relay", Env: "prod", Version: "test", Node: "A",
		Format: "json", Output: &out})
	// the relay's own stack: the request id middleware in front of the proxy
	front := httptest.NewServer(middleware.RequestID(green.deps.Handler()))
	defer front.Close()

	h := simhome.Start(7, blue.endpoint(), api.URL, simhome.Options{})
	waitOn(t, "H on blue", func() bool { _, ok := dir.Row(h.Slug); return ok })
	lookup.saw(h.Slug, time.Now())
	serverID := h.Slug // simLookup's server id is its slug
	defer h.Stop()
	// blue still holds the tunnel, but its peer listener is gone: green's forward fails
	blue.peer.Close()
	const rid = "0192f0c4-1111-7000-8000-000000000001"
	resp := getVia(t, front, h.Slug, "/api/health?token=s3cret", map[string]string{"X-Request-Id": rid})
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	var line map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil && m["event"] == "relay_peer_unreachable" {
			line = m
		}
	}
	if line == nil {
		t.Fatalf("no relay_peer_unreachable line in:\n%s", out.String())
	}
	if line["service"] != "cloud.relay" || line["node"] != "A" || line["request_id"] != rid ||
		line["home_h"] != mlog.UserHash(key, serverID) {
		t.Fatalf("the line: %v (want home_h %s)", line, mlog.UserHash(key, serverID))
	}
	all := out.String()
	for _, gone := range []string{h.Slug, serverID, "s3cret", "@"} {
		if gone != "" && strings.Contains(all, gone) {
			t.Errorf("%q is in the relay's log:\n%s", gone, all)
		}
	}
}
