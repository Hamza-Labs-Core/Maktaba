package link

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	f := Frame{ID: 7, Kind: KindChunk, Class: ClassRPC, Payload: []byte("abc")}
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	g, err := Decode(b)
	if err != nil || g.ID != 7 || g.Kind != KindChunk || g.Class != ClassRPC || !bytes.Equal(g.Payload, []byte("abc")) {
		t.Fatalf("%+v %v", g, err)
	}
	for _, bad := range [][]byte{nil, b[:5], append(append([]byte{}, b...), 'x'), {0, 0, 0, 1, 9, 0, 0, 0, 0, 0}} {
		if _, err := Decode(bad); err == nil {
			t.Fatalf("decoded %x", bad)
		}
	}
}

// A control frame carries at most 16 KiB; a header claiming more is refused.
func TestControlPayloadCap(t *testing.T) {
	if _, err := (Frame{Kind: KindChunk, Class: ClassRPC, Payload: make([]byte, MaxPayload+1)}).Encode(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("encode: %v", err)
	}
	ok, _ := (Frame{Kind: KindChunk, Class: ClassRPC, Payload: make([]byte, MaxPayload)}).Encode()
	ok[6], ok[7] = 0xff, 0xff // claims more than 16 KiB
	if _, err := Decode(ok); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("decode: %v", err)
	}
	if got := split(make([]byte, 2*MaxPayload+1)); len(got) != 3 || len(got[2]) != 1 {
		t.Fatalf("split: %d parts", len(got))
	}
}

// A flood of rpc frames never delays a control frame by more than one message.
func TestControlFramingClassOrder(t *testing.T) {
	s := newSender()
	ctx := context.Background()
	for range 60 {
		if err := s.send(ctx, Frame{Kind: KindChunk, Class: ClassRPC, Payload: []byte("rpc")}); err != nil {
			t.Fatal(err)
		}
	}
	// the writer has taken one rpc frame when a command comes
	first, _ := s.next()
	if f, _ := Decode(first); f.Class != ClassRPC {
		t.Fatal("expected an rpc frame first")
	}
	if err := s.send(ctx, Frame{Kind: KindEvent, Class: ClassControl, Payload: []byte(`{"t":"stream.offer"}`)}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.next()
	if f, _ := Decode(b); f.Class != ClassControl {
		t.Fatalf("the control frame waited behind rpc frames (got class %d)", f.Class)
	}
	s.close()
	if err := s.send(ctx, Frame{Kind: KindEvent, Class: ClassControl}); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
}

// The framing never panics on what a peer sends, and what it accepts encodes back.
func FuzzDecode(f *testing.F) {
	good, _ := (Frame{ID: 1, Kind: KindReq, Class: ClassRPC, Payload: []byte(`{"method":"GET","uri":"/"}`)}).Encode()
	f.Add(good)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		fr, err := Decode(b)
		if err == nil {
			again, err := fr.Encode()
			if err != nil || !bytes.Equal(again, b) {
				t.Fatalf("re-encode: %x vs %x (%v)", again, b, err)
			}
		}
	})
}
