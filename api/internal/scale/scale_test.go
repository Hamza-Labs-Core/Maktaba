package scale

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestShardSingleHost(t *testing.T) {
	if Shard("any-key", 0) != 0 || Shard("any-key", 1) != 0 {
		t.Fatal("single-host fallback should be 0")
	}
}

func TestShardDeterministic(t *testing.T) {
	a := Shard("user-1", 8)
	b := Shard("user-1", 8)
	if a != b {
		t.Fatalf("non-deterministic: %d / %d", a, b)
	}
	if a < 0 || a >= 8 {
		t.Fatalf("out of range: %d", a)
	}
}

func TestShardDistributes(t *testing.T) {
	hits := make(map[int]int)
	for i := 0; i < 1000; i++ {
		hits[Shard("u-"+string(rune('a'+(i%26)))+itoa(i), 4)]++
	}
	if len(hits) != 4 {
		t.Fatalf("expected all 4 shards to receive traffic, got %d", len(hits))
	}
}

// itoa avoids strconv import in tests.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var out []byte
	for i > 0 {
		out = append([]byte{byte('0' + i%10)}, out...)
		i /= 10
	}
	return string(out)
}

func TestInMemoryBusPublishSubscribe(t *testing.T) {
	b := NewInMemoryBus()
	defer b.Close()
	ch, cancel := b.Subscribe("job-updates")
	defer cancel()

	if err := b.Publish(context.Background(), "job-updates", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		if string(e.Payload) != "hello" {
			t.Fatalf("payload: %s", e.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestInMemoryBusDropsOnSlowSubscriber(_ *testing.T) {
	b := NewInMemoryBus()
	defer b.Close()
	_, _ = b.Subscribe("noisy")
	// Don't read — verify publishes don't deadlock.
	for i := 0; i < 100; i++ {
		_ = b.Publish(context.Background(), "noisy", []byte("x"))
	}
}

func TestInMemoryBusUnsubscribeClosesChannel(t *testing.T) {
	b := NewInMemoryBus()
	defer b.Close()
	ch, cancel := b.Subscribe("t")
	cancel()
	_, ok := <-ch
	if ok {
		t.Fatal("expected closed channel")
	}
}

func TestLimiterCapPanicsAtZero(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewLimiter(0)
}

func TestLimiterTryAcquireExhaust(t *testing.T) {
	l := NewLimiter(2)
	_ = l.TryAcquire()
	_ = l.TryAcquire()
	if err := l.TryAcquire(); !errors.Is(err, ErrLimiterFull) {
		t.Fatalf("expected ErrLimiterFull, got %v", err)
	}
	l.Release()
	if err := l.TryAcquire(); err != nil {
		t.Fatalf("Release did not free a slot: %v", err)
	}
}

func TestLimiterAcquireRespectsCtx(t *testing.T) {
	l := NewLimiter(1)
	_ = l.TryAcquire()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := l.Acquire(ctx); err == nil {
		t.Fatal("expected ctx timeout")
	}
}

func TestLimiterConcurrent(t *testing.T) {
	l := NewLimiter(4)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.Acquire(context.Background())
			time.Sleep(time.Millisecond)
			l.Release()
		}()
	}
	wg.Wait()
	if l.InUse() != 0 {
		t.Fatalf("leaked tokens: %d", l.InUse())
	}
}
