package main

import "testing"

func TestBitrates(t *testing.T) {
	for in, want := range map[string]int{"5M": 5_000_000, "2.5M": 2_500_000, "8M": 8_000_000, "640k": 640_000, "1000": 1000} {
		if got, err := parseBits(in); err != nil || got != want {
			t.Errorf("%s: %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "fast", "-5M", "0"} {
		if _, err := parseBits(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
