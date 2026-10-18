package discovery

import (
	"context"
	"reflect"
	"testing"
)

func TestTXTRecordsSorted(t *testing.T) {
	got := TXTRecords(map[string]string{"role": "home", "join": "0", "id": "6f1c2a3b"})
	want := []string{"id=6f1c2a3b", "join=0", "role=home"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TXT = %v", got)
	}
}

// Registration itself needs multicast, so only the argument checks run
// here; the TXT shape is covered by the cluster Advertiser tests.
func TestZeroconfPublisherRejectsIncompleteService(t *testing.T) {
	z := NewZeroconfPublisher()
	if err := z.Publish(context.Background(), Service{Port: 8080}); err == nil {
		t.Error("missing instance accepted")
	}
	if err := z.Publish(context.Background(), Service{Instance: "Maktaba (x)"}); err == nil {
		t.Error("missing port accepted")
	}
	if err := z.Close(); err != nil {
		t.Error(err)
	}
}
