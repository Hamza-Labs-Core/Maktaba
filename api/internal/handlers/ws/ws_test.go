package ws

import (
	"testing"
	"time"
)

func TestHub_FanOut(t *testing.T) {
	h := NewHub()
	s1 := h.Subscribe("jobs")
	s2 := h.Subscribe("jobs")
	other := h.Subscribe("library:abc")
	go h.Publish("jobs", Event{Type: "jobs.new", Payload: map[string]any{"id": "x"}})
	for i, s := range []*Subscriber{s1, s2} {
		select {
		case e := <-s.C:
			if e.Type != "jobs.new" {
				t.Errorf("sub %d wrong event: %v", i, e)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("sub %d timed out", i)
		}
	}
	select {
	case <-other.C:
		t.Error("library subscriber should not have received jobs event")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestHub_SlowConsumerDropped(t *testing.T) {
	h := NewHub()
	s := h.Subscribe("jobs")
	for i := 0; i < 1001; i++ {
		h.Publish("jobs", Event{Type: "noise"})
	}
	select {
	case <-s.Done:
	case <-time.After(200 * time.Millisecond):
		t.Errorf("expected slow consumer to be dropped")
	}
}
