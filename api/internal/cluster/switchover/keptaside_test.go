package switchover

import (
	"bytes"
	"testing"

	"golang.org/x/crypto/curve25519"
)

// The report an old home's supervisor sealed (cmd/maktaba-server/internal/switchover,
// TestTheReportVectorOpens: the same vector) opens here, and only for its term.
func TestTheOldHomesReportOpensHere(t *testing.T) {
	cluster := bytes.Repeat([]byte{7}, 32)
	oldPub, err := curve25519.X25519(bytes.Repeat([]byte{9}, 32), curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	const sealed = "1CtyYpAaNuKahUBwDeKXtISXnI+U53Wzqy5D7yJ4wjXMbqo0lqakMa5cbbYpWgI="
	got, err := openReport(cluster, oldPub, 7, sealed)
	if err != nil || string(got) != `{"id":"r","term":7}` {
		t.Fatalf("the vector: %q %v", got, err)
	}
	if _, err := openReport(cluster, oldPub, 8, sealed); err == nil {
		t.Fatal("it opened for another term")
	}
	if _, err := openReport(bytes.Repeat([]byte{8}, 32), oldPub, 7, sealed); err == nil {
		t.Fatal("it opened with another cluster's key")
	}
}
