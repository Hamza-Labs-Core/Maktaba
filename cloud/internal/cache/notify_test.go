package cache

import (
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type rec struct {
	mu     sync.Mutex
	got    []string
	resets int
}

func (r *rec) Invalidate(kind, id string) {
	r.mu.Lock()
	r.got = append(r.got, kind+"/"+id)
	r.mu.Unlock()
}
func (r *rec) Reset() { r.mu.Lock(); r.resets++; r.mu.Unlock() }

// Each channel's payload reaches every cache as its kind; a payload with no kind is dropped.
func TestDispatch(t *testing.T) {
	var b Bus
	a, c := &rec{}, &rec{}
	b.Add(a)
	b.Add(c)
	b.Dispatch(stores.ChannelTierChanged, "home-1")
	b.Dispatch(stores.ChannelServerChanged, "home-2")
	b.Dispatch(Channel, "tier:home-3")
	b.Dispatch(Channel, "no-kind")
	b.Dispatch(Channel, ":x")
	b.Dispatch("other", "x")
	want := []string{"tier/home-1", "server/home-2", "tier/home-3"}
	for _, r := range []*rec{a, c} {
		if len(r.got) != len(want) {
			t.Fatalf("got %v, want %v", r.got, want)
		}
		for i := range want {
			if r.got[i] != want[i] {
				t.Fatalf("got %v, want %v", r.got, want)
			}
		}
	}
	b.ResetAll()
	if a.resets != 1 || c.resets != 1 {
		t.Fatal("ResetAll missed a cache")
	}
	if inv, res := b.Counts(); inv != 3 || res != 1 {
		t.Fatalf("counts %d %d", inv, res)
	}
}

func TestFuncsNil(t *testing.T) {
	Funcs{}.Invalidate("tier", "x")
	Funcs{}.Reset()
}
