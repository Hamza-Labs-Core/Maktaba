package router

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

func TestWireExtensionTesters(t *testing.T) {
	q := &queues.Handler{}
	WireExtensionTesters(q, nil, nil, nil)
	for _, name := range []string{"plex", "web_search"} {
		if q.Testers[name] == nil {
			t.Errorf("no tester for %s", name)
		}
	}
	// dizqueTV is no extension of Maktaba's any more
	if q.Testers["dizquetv"] != nil {
		t.Error("a tester for the removed dizquetv extension")
	}
	// WireP32Remote keeps the Cloudflare tester and adds these
	WireP32Remote(q, P32RemoteDeps{NoLoop: true})
	for _, name := range []string{"plex", "web_search", queues.CloudflareExtension} {
		if q.Testers[name] == nil {
			t.Errorf("after WireP32Remote: no tester for %s", name)
		}
	}
}
