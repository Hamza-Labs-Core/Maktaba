package router

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Story 50.25: a claimed home honours the revocation list once it holds a broadcast signing key
// (the release's or MAKTABA_BROADCAST_PUBKEYS); with none, or an unreadable set, it does not.
func TestMountBroadcastWiresTheRevocationGuard(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	keys := `{"k1":"` + base64.RawURLEncoding.EncodeToString(pub) + `"}`
	svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{AgentAddr: "127.0.0.1:1", NoLoop: true, RevocationKeys: keys})
	if !svc.Revocations.Enabled() {
		t.Fatal("a claimed home with a key does not honour the list")
	}
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{AgentAddr: "127.0.0.1:1", NoLoop: true}); svc.Revocations.Enabled() {
		t.Fatal("a home with no key honours a list it can't check")
	}
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{AgentAddr: "127.0.0.1:1", NoLoop: true, RevocationKeys: "{"}); svc.Revocations.Enabled() {
		t.Fatal("an unreadable key set is honoured")
	}
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{NoLoop: true, RevocationKeys: keys}); svc.Revocations.Enabled() {
		t.Fatal("an unclaimed home has a revocation source")
	}
}

// Story 50.24: a claimed home asks Maktaba Cloud for its takedowns and records through the same
// agent; an unclaimed one has nothing to ask.
func TestMountBroadcastWiresTheModerationCloud(t *testing.T) {
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{AgentAddr: "127.0.0.1:1", NoLoop: true}); svc.Moderation == nil {
		t.Fatal("a claimed home has no moderation cloud")
	}
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{NoLoop: true}); svc.Moderation != nil {
		t.Fatal("an unclaimed home asks for takedowns")
	}
}
