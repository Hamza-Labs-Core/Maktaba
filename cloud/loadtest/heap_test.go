package loadtest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The relays' live heaps add up; a relay with no series (a build from before it) says so with
// ErrNoLiveHeap, so load-small writes no heap figure rather than a wrong one. The relay answers
// /metrics on its bare host only, so the Host is the one given.
func TestLiveHeapSumsTheRelays(t *testing.T) {
	serve := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != "relay.test" {
				http.NotFound(w, r)
				return
			}
			_, _ = fmt.Fprint(w, body)
		}))
	}
	blue := serve("# TYPE maktaba_go_heap_live_bytes gauge\nmaktaba_go_heap_live_bytes 3000000\nmaktaba_relay_active_tunnels 500\n")
	defer blue.Close()
	green := serve("maktaba_go_heap_live_bytes 2000000\n")
	defer green.Close()
	old := serve("maktaba_relay_active_tunnels 0\n")
	defer old.Close()

	got, err := LiveHeap{URLs: []string{blue.URL, green.URL}, Host: "relay.test"}.Bytes(context.Background())
	if err != nil || got != 5_000_000 {
		t.Fatalf("%v %v, want 5000000", got, err)
	}
	if _, err := (LiveHeap{URLs: []string{blue.URL, old.URL}, Host: "relay.test"}).Bytes(context.Background()); !errors.Is(err, ErrNoLiveHeap) {
		t.Fatalf("a relay with no live heap: %v", err)
	}
	if _, err := (LiveHeap{URLs: []string{blue.URL}}).Bytes(context.Background()); err == nil {
		t.Fatal("a 404 read as a heap")
	}
}

// Each round is the median of its samples and the run the median of its rounds, less the heap
// before the homes, per tunnel: one sample caught at a high (a collection late, segments in
// flight) moves neither.
func TestHeapRoundsMedians(t *testing.T) {
	r := HeapRounds{Before: 10e6}
	for _, round := range [][]float64{
		{40e6, 41e6, 90e6, 40e6, 39e6}, // one sample at a high
		{41e6, 40e6, 40e6},
		{},                       // a round with no sample is dropped
		{42e6, 41e6, 41e6, 41e6}, // even: the middle two
	} {
		for _, s := range round {
			r.Add(s)
		}
		r.EndRound()
	}
	got, per := r.PerTunnel(1000)
	if len(per) != 3 || per[0] != 30_000 || per[1] != 30_000 || per[2] != 31_000 || got != 30_000 {
		t.Fatalf("%v %v", got, per)
	}
	if got, per := (&HeapRounds{}).PerTunnel(1000); got != 0 || per != nil {
		t.Fatalf("no rounds: %v %v", got, per)
	}
	if got, _ := r.PerTunnel(0); got != 0 {
		t.Fatalf("no tunnels: %v", got)
	}
}
