package relay

import (
	"bytes"
	"encoding/json"
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
		if !bytes.Equal(got.Payload, f.Payload) && (len(got.Payload) != 0 || len(f.Payload) != 0) {
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

func TestEncodeFrameRefusesOversize(t *testing.T) {
	if _, err := encodeFrame(Frame{Kind: KindResponseBody, Payload: make([]byte, MaxFrameBytes+1)}); err != ErrFrameTooLarge {
		t.Fatalf("oversize frame: %v", err)
	}
	b, err := encodeFrame(Frame{Kind: KindPing, StreamID: 7, Payload: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	f, err := ReadFrame(bytes.NewReader(b))
	if err != nil || f.Kind != KindPing || f.StreamID != 7 || string(f.Payload) != "hi" {
		t.Fatalf("round trip %+v %v", f, err)
	}
}

// T03 (story 50.03): DRAIN and TICKET round-trip on the wire; unknown fields
// are ignored.
func TestDrainAndTicketFrames(t *testing.T) {
	p, _ := json.Marshal(DrainPayload{Reason: DrainDeploy, WithinMS: 5000, DeadlineMS: 120000, Ticket: "tk"})
	for _, f := range []Frame{{Kind: KindDrain, Payload: p}, {Kind: KindTicket, Payload: []byte(`{"ticket":"tk2"}`)}} {
		var buf bytes.Buffer
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatal(err)
		}
		got, err := ReadFrame(&buf)
		if err != nil || got.Kind != f.Kind || !bytes.Equal(got.Payload, f.Payload) {
			t.Fatalf("round trip %#x: %+v %v", f.Kind, got, err)
		}
	}
	var d DrainPayload
	if err := json.Unmarshal([]byte(`{"reason":"shutdown","within_ms":1,"deadline_ms":2,"ticket":"x","later":true}`), &d); err != nil {
		t.Fatal(err)
	}
	if d != (DrainPayload{Reason: DrainShutdown, WithinMS: 1, DeadlineMS: 2, Ticket: "x"}) {
		t.Errorf("decoded %+v", d)
	}
	if KindDrain != 0x22 || KindTicket != 0x23 {
		t.Error("frame numbers changed")
	}
}
