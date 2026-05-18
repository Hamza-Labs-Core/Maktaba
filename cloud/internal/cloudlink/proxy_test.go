package cloudlink

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestParseRequest_PreservesMethodHeadersHostBody verifies the client
// faithfully reconstructs the request the cloud serialized in
// relay/tunnel.go's "METHOD URI HTTP/1.1\r\nHost:..\r\n..\r\n\r\n"
// preamble form, retargeted at the loopback base.
func TestParseRequest_PreservesMethodHeadersHostBody(t *testing.T) {
	var head bytes.Buffer
	fmt.Fprintf(&head, "POST /api/x?q=1 HTTP/1.1\r\n")
	fmt.Fprintf(&head, "Host: acme.maktaba.app\r\n")
	fmt.Fprintf(&head, "X-Custom: yes\r\n")
	head.WriteString("\r\n")

	req, err := parseRequest(context.Background(), head.Bytes(), []byte("hello"), "http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("parseRequest: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Errorf("method = %q", req.Method)
	}
	if req.URL.String() != "http://127.0.0.1:8080/api/x?q=1" {
		t.Errorf("url = %q", req.URL.String())
	}
	if req.Host != "acme.maktaba.app" {
		t.Errorf("host = %q, want preserved original", req.Host)
	}
	if req.Header.Get("X-Custom") != "yes" {
		t.Errorf("custom header lost")
	}
	if req.ContentLength != 5 {
		t.Errorf("content-length = %d, want 5", req.ContentLength)
	}
	b, _ := io.ReadAll(req.Body)
	if string(b) != "hello" {
		t.Errorf("body = %q", string(b))
	}
}

// captureSink records frames written by the proxy so we can assert the
// response framing without a tunnel.
type captureSink struct{ frames []Frame }

func (c *captureSink) WriteFrame(f Frame) error {
	c.frames = append(c.frames, f)
	return nil
}

// TestProxyServe_StreamsResponseFrames asserts a real loopback response
// is serialized as RESPONSE_HEAD then RESPONSE_BODY chunks then an
// empty RESPONSE_BODY EOF — exactly what relay/tunnel.go's streamBody
// expects to read.
func TestProxyServe_StreamsResponseFrames(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body-bytes"))
	}))
	defer local.Close()

	var head bytes.Buffer
	head.WriteString("GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	asm := &reqAssembly{head: head.Bytes()}

	cap := &captureSink{}
	p := &LocalProxy{BaseURL: local.URL}
	if err := p.serve(context.Background(), 7, asm, cap); err != nil {
		t.Fatalf("serve: %v", err)
	}

	if len(cap.frames) < 2 {
		t.Fatalf("expected >=2 frames, got %d", len(cap.frames))
	}
	if cap.frames[0].Kind != KindResponseHead || cap.frames[0].StreamID != 7 {
		t.Fatalf("frame[0] = kind %#x stream %d, want RESPONSE_HEAD/7", cap.frames[0].Kind, cap.frames[0].StreamID)
	}
	last := cap.frames[len(cap.frames)-1]
	if last.Kind != KindResponseBody || len(last.Payload) != 0 {
		t.Fatalf("last frame = kind %#x len %d, want empty RESPONSE_BODY (EOF)", last.Kind, len(last.Payload))
	}
	var bodyBuf bytes.Buffer
	for _, f := range cap.frames[1:] {
		if f.Kind == KindResponseBody {
			bodyBuf.Write(f.Payload)
		}
	}
	if bodyBuf.String() != "body-bytes" {
		t.Fatalf("reassembled body = %q, want body-bytes", bodyBuf.String())
	}
}

func TestProxyServe_BadGatewayOnLoopbackFailure(t *testing.T) {
	var head bytes.Buffer
	head.WriteString("GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	cap := &captureSink{}
	p := &LocalProxy{BaseURL: "http://127.0.0.1:1", Client: &http.Client{}}
	if err := p.serve(context.Background(), 1, &reqAssembly{head: head.Bytes()}, cap); err != nil {
		t.Fatalf("serve returned err (should emit 502 frame instead): %v", err)
	}
	if cap.frames[0].Kind != KindResponseHead || !bytes.Contains(cap.frames[0].Payload, []byte("502")) {
		t.Fatalf("expected 502 RESPONSE_HEAD, got %q", string(cap.frames[0].Payload))
	}
	if cap.frames[len(cap.frames)-1].Kind != KindCloseStream {
		t.Fatalf("expected trailing CLOSE_STREAM on failure")
	}
}
