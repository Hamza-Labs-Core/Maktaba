package push

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type countRecorder struct {
	mu         sync.Mutex
	ok, failed int
}

func (c *countRecorder) RecordPush(ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ok {
		c.ok++
	} else {
		c.failed++
	}
}

type failDriver struct{}

func (failDriver) Name() string { return "fail" }
func (failDriver) Send(context.Context, string, Notification) error {
	return errors.New("bad token")
}

// Stub audit #36: every device delivery is counted for the Epic 30
// push_sent / push_failed metrics, the same way the log records it: a
// driver success is sent; a driver error or a platform with no driver
// failed.
func TestFanOutRecordsEachDelivery(t *testing.T) {
	rec := &countRecorder{}
	fake := &FakeDriver{}
	d := &Dispatcher{APNs: fake, FCM: failDriver{}, Metrics: rec}
	d.fanOut(context.Background(), Notification{UserID: "u1", Title: "hi"}, []target{
		{platform: "ios", token: "a"},
		{platform: "ios", token: "b"},
		{platform: "android", token: "c"},
		{platform: "symbian", token: "d"},
	})
	if rec.ok != 2 || rec.failed != 2 {
		t.Fatalf("recorded ok=%d failed=%d, want 2/2", rec.ok, rec.failed)
	}
	if len(fake.Sent) != 2 {
		t.Fatalf("fake driver sent %d, want 2", len(fake.Sent))
	}
}

func TestFanOutWithoutRecorder(t *testing.T) {
	d := &Dispatcher{APNs: &FakeDriver{}}
	d.fanOut(context.Background(), Notification{UserID: "u1"}, []target{{platform: "ios", token: "a"}})
}
