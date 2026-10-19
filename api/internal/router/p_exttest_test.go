package router

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

func TestWireExtensionTesters(t *testing.T) {
	q := &queues.Handler{}
	WireExtensionTesters(q, nil, nil, nil)
	for _, name := range []string{"plex", "dizquetv"} {
		if q.Testers[name] == nil {
			t.Errorf("no tester for %s", name)
		}
	}
	// WireP32Remote keeps the Cloudflare tester and adds these
	WireP32Remote(q, P32RemoteDeps{NoLoop: true})
	for _, name := range []string{"plex", "dizquetv", queues.CloudflareExtension} {
		if q.Testers[name] == nil {
			t.Errorf("after WireP32Remote: no tester for %s", name)
		}
	}
}
