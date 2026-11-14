//go:build linux

package hostload

import "testing"

func TestParseLoadAvg(t *testing.T) {
	got, ok := parseLoadAvg("512.31 498.02 301.77 31/2890 12345\n")
	if !ok || got != [3]float64{512.31, 498.02, 301.77} {
		t.Fatalf("%v %v", got, ok)
	}
	if _, ok := parseLoadAvg("x y"); ok {
		t.Fatal("a short line parsed")
	}
}
