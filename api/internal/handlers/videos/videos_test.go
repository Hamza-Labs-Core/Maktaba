package videos

import "testing"

func TestBidiIsolate(t *testing.T) {
	got := bidiIsolate("hello")
	if got != "\u2068hello\u2069" {
		t.Errorf("expected isolate wrap, got %q", got)
	}
}

func TestBidiIsolate_EmptyPasses(t *testing.T) {
	if bidiIsolate("") != "" {
		t.Errorf("empty must remain empty")
	}
}

func TestItoa(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1", 10: "10", -7: "-7", 123: "123"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q want %q", in, got, want)
		}
	}
}

func TestPidx(t *testing.T) {
	if pidx(3) != "$3" {
		t.Errorf("got %q", pidx(3))
	}
}
