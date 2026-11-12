package graphql

// Epic 57 story 57.03: a GraphQL subscription is one live connection, filtered by
// the hub's guard exactly as an SSE feed is: an event the guard withholds is not
// mapped or sent, and the subscription ends when the guard says so.

import (
	"context"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

func TestSubscribeAsksTheHubGuard(t *testing.T) {
	hub := ws.NewHub()
	child := &principal.Principal{UserID: "00000000-0000-4000-8000-0000000000c1"}
	var asked []string
	hub.Guard = func(p *principal.Principal, channel string) ws.Guard {
		if p != child || channel != "library:a" {
			t.Errorf("the guard was made for %v on %q", p, channel)
		}
		return func(_ context.Context, e ws.Event) (bool, bool) {
			asked = append(asked, e.Type)
			return e.Type == "shown", e.Type == "end"
		}
	}
	ctx, cancel := context.WithCancel(principal.WithPrincipal(context.Background(), child))
	defer cancel()
	mapped := 0
	out, err := subscribe(ctx, hub, "library:a", func(e ws.Event) (string, bool) {
		mapped++
		return e.Type, true
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"hidden", "shown", "hidden", "end", "shown"} {
		hub.Publish("library:a", ws.Event{Type: typ})
	}
	var got []string
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case v, ok := <-out:
			if !ok {
				break loop
			}
			got = append(got, v)
		case <-deadline:
			t.Fatal("the subscription did not end when the guard ended it")
		}
	}
	if len(got) != 1 || got[0] != "shown" || mapped != 1 {
		t.Errorf("sent %v (mapped %d events), want only the one shown before the end", got, mapped)
	}
	if len(asked) != 4 {
		t.Errorf("the guard was asked about %v, want every event up to the end", asked)
	}
	if n := hub.SubscriberCount("library:a"); n != 0 {
		t.Errorf("%d subscribers left after the end", n)
	}

	// a hub that guards and gives a connection no filter sends it nothing
	hub.Guard = func(*principal.Principal, string) ws.Guard { return nil }
	out, err = subscribe(ctx, hub, "home", func(e ws.Event) (string, bool) { return e.Type, true })
	if err != nil {
		t.Fatal(err)
	}
	hub.Publish("home", ws.Event{Type: "shown"})
	select {
	case v, ok := <-out:
		if ok {
			t.Errorf("a connection with no filter was sent %q", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a connection with no filter did not end")
	}
}
