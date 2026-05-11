package relay

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []Frame{
		{KindAuth, 0, []byte(`{"server_id":"x"}`)},
		{KindRequestHead, 42, []byte("GET / HTTP/1.1\r\nHost: example\r\n\r\n")},
		{KindResponseBody, 7, []byte{}},
		{KindPing, 0, nil},
	}
	var buf bytes.Buffer
	for _, f := range cases {
		buf.Reset()
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatalf("WriteFrame(%v): %v", f.Kind, err)
		}
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if got.Kind != f.Kind || got.StreamID != f.StreamID {
			t.Errorf("kind/stream mismatch: got=%v want=%v", got, f)
		}
		if !bytes.Equal(got.Payload, f.Payload) && !(len(got.Payload) == 0 && len(f.Payload) == 0) {
			t.Errorf("payload mismatch: %q vs %q", got.Payload, f.Payload)
		}
	}
}

func TestReadFrame_Empty(t *testing.T) {
	_, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Errorf("ReadFrame(empty) = %v, want io.EOF", err)
	}
}

func TestWriteFrame_TooLarge(t *testing.T) {
	f := Frame{Kind: KindResponseBody, Payload: make([]byte, MaxFrameBytes+1)}
	if err := WriteFrame(&bytes.Buffer{}, f); err != ErrFrameTooLarge {
		t.Errorf("WriteFrame(huge) = %v, want %v", err, ErrFrameTooLarge)
	}
}
