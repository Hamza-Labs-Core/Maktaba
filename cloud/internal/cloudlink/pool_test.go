package cloudlink

import (
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

func assignment(here string) *RelaysPayload {
	return &RelaysPayload{Primary: relay.RelayTarget{ID: "r3", Host: "r3.relay.test"},
		Standby: &relay.RelayTarget{ID: "r5", Host: "r5.relay.test"}, Here: here}
}

// Story 50.16: a home outside a pool dials its endpoint; in one it dials its primary, then its
// standby, then the endpoint; after its primary tunnel drops, the relay that dropped goes last.
func TestPrimaryTargets(t *testing.T) {
	s := &Supervisor{}
	if got := s.primaryTargets(); !slices.Equal(got, []string{""}) {
		t.Fatalf("no pool: %q", got)
	}
	s.adoptPrimary(DialResult{Relays: assignment("r3")})
	if got := s.primaryTargets(); !slices.Equal(got, []string{"r3.relay.test", "r5.relay.test", ""}) {
		t.Fatalf("in a pool: %q", got)
	}
	s.primaryDropped(DialResult{Relays: assignment("r3")}) // dialled at the endpoint, on r3
	if got := s.primaryTargets(); !slices.Equal(got, []string{"r5.relay.test", "r3.relay.test", ""}) {
		t.Fatalf("after r3 dropped: %q", got)
	}
	s.primaryDropped(DialResult{Host: "r5.relay.test"})
	if got := s.primaryTargets(); !slices.Equal(got, []string{"r3.relay.test", "r5.relay.test", ""}) {
		t.Fatalf("after r5 dropped: %q", got)
	}
	// a relay that runs no pool: back to the endpoint alone
	s.adoptPrimary(DialResult{})
	if got := s.primaryTargets(); !slices.Equal(got, []string{""}) {
		t.Fatalf("pool off: %q", got)
	}
}

// The standby belongs on the assignment's standby relay, never on the relay the primary is on
// (the home moved its primary there) and not before the primary is up.
func TestStandbyTarget(t *testing.T) {
	s := &Supervisor{}
	if got := s.standbyTarget(); got.Host != "" {
		t.Fatalf("no pool: %+v", got)
	}
	s.adoptPrimary(DialResult{Relays: assignment("r3")})
	if got := s.standbyTarget(); got.ID != "r5" || got.Host != "r5.relay.test" {
		t.Fatalf("primary on r3: %+v", got)
	}
	s.adoptPrimary(DialResult{Relays: assignment("r5")}) // promoted onto its standby's relay
	if got := s.standbyTarget(); got.Host != "" {
		t.Fatalf("primary on r5 (its standby relay): %+v", got)
	}
	s.adoptRelays(&RelaysPayload{Primary: relay.RelayTarget{ID: "r5", Host: "r5.relay.test"},
		Standby: &relay.RelayTarget{ID: "r1", Host: "r1.relay.test"}})
	if got := s.standbyTarget(); got.ID != "r1" {
		t.Fatalf("after RELAYS: %+v", got)
	}
	s.primaryDropped(DialResult{Host: "r5.relay.test"})
	if got := s.standbyTarget(); got.Host != "" {
		t.Fatalf("no primary: %+v", got)
	}
	// an empty assignment changes nothing
	s.adoptRelays(&RelaysPayload{})
	if got := s.primaryTargets(); got[0] != "r1.relay.test" {
		t.Fatalf("an empty RELAYS replaced the assignment: %q", got)
	}
}

// The endpoint's host is replaced by the member's, keeping the scheme and path.
func TestEndpointAt(t *testing.T) {
	d := &Dialer{Endpoint: "wss://relay.maktaba.tv/v1/relay/ws"}
	if got := d.endpointAt("r3.relay.maktaba.tv"); got != "wss://r3.relay.maktaba.tv/v1/relay/ws" {
		t.Fatal(got)
	}
	if got := d.endpointAt(""); got != d.Endpoint {
		t.Fatal(got)
	}
	d.Endpoint = "ws://127.0.0.1:18080/v1/relay/ws"
	if got := d.endpointAt("r1.relay.test"); got != "ws://r1.relay.test/v1/relay/ws" {
		t.Fatal(got)
	}
}
