package cloudlink

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// LocalProxy turns a reassembled inbound HTTP request into a call
// against the on-prem box's own loopback API and serializes the
// response back onto the tunnel.
//
// This is the exact inverse of cloud/internal/relay/tunnel.go's Proxy:
// the cloud writes REQUEST_HEAD/BODY and reads RESPONSE_HEAD/BODY; we
// read the former and write the latter for the same stream id.
type LocalProxy struct {
	// BaseURL is the loopback API root, e.g. http://127.0.0.1:8080.
	BaseURL string
	// Client performs the loopback call. If nil, a dedicated client
	// (see defaultLoopbackClient) is used. We deliberately do NOT fall
	// back to http.DefaultClient: a long-lived relay agent must not
	// share the process-global transport's connection pool for its
	// loopback calls — that couples unrelated HTTP traffic and makes
	// the agent's behaviour depend on global state.
	Client *http.Client

	once       sync.Once
	cachedClnt *http.Client
}

// defaultLoopbackClient builds the per-LocalProxy HTTP client used for
// loopback calls. It is isolated (its own *http.Transport) and bounds
// connect/idle so a stuck local API cannot pin a goroutine forever.
func defaultLoopbackClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          16,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

func (p *LocalProxy) httpClient() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	p.once.Do(func() { p.cachedClnt = defaultLoopbackClient() })
	return p.cachedClnt
}

// frameSink is the subset of FrameConn the proxy needs to emit the
// response. The multiplexer serializes all writes through one mutex
// (see multiplex.go) so this is safe to call from per-stream goroutines.
type frameSink interface {
	WriteFrame(Frame) error
}

// reqAssembly is the per-stream inbound buffer: a REQUEST_HEAD followed
// by zero or more REQUEST_BODY frames terminated by an empty BODY.
type reqAssembly struct {
	head []byte
	body bytes.Buffer
}

// parseRequest reconstructs an *http.Request from the HTTP/1.1-shaped
// preamble the cloud wrote (relay/tunnel.go writes
// "METHOD URI HTTP/1.1\r\nHost: ...\r\n<headers>\r\n\r\n").
func parseRequest(ctx context.Context, head, body []byte, baseURL string) (*http.Request, error) {
	br := bufio.NewReader(bytes.NewReader(head))
	r, err := http.ReadRequest(br)
	if err != nil {
		return nil, fmt.Errorf("cloudlink: parse request preamble: %w", err)
	}
	// Re-target at the loopback API. RequestURI must be cleared before a
	// request can be used as a client request (net/http contract).
	target := baseURL + r.URL.RequestURI()
	out, err := http.NewRequestWithContext(ctx, r.Method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cloudlink: build loopback request: %w", err)
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			out.Header.Add(k, v)
		}
	}
	// Preserve the original Host the cloud forwarded; the loopback API
	// may route on it. net/http special-cases Host outside Header.
	if r.Host != "" {
		out.Host = r.Host
	}
	out.ContentLength = int64(len(body))
	return out, nil
}

// serve runs one inbound request: parse → loopback call → stream the
// response frames back. On any local failure we still emit a
// RESPONSE_HEAD/CLOSE_STREAM so the cloud's stream does not hang.
func (p *LocalProxy) serve(ctx context.Context, streamID uint32, asm *reqAssembly, out frameSink) error {
	req, err := parseRequest(ctx, asm.head, asm.body.Bytes(), p.BaseURL)
	if err != nil {
		return p.fail(streamID, out, http.StatusBadGateway, err)
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return p.fail(streamID, out, http.StatusBadGateway, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var head bytes.Buffer
	fmt.Fprintf(&head, "HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode))
	for k, vs := range resp.Header {
		for _, v := range vs {
			fmt.Fprintf(&head, "%s: %s\r\n", k, v)
		}
	}
	head.WriteString("\r\n")
	if err := out.WriteFrame(Frame{Kind: KindResponseHead, StreamID: streamID, Payload: head.Bytes()}); err != nil {
		return err
	}

	// Stream the body in <=32 KiB chunks (matches the cloud side's
	// chunk size) so we never buffer a whole 1 GiB response. An empty
	// RESPONSE_BODY frame signals EOF, mirroring the request path.
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if werr := out.WriteFrame(Frame{Kind: KindResponseBody, StreamID: streamID, Payload: chunk}); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			return out.WriteFrame(Frame{Kind: KindResponseBody, StreamID: streamID})
		}
		if rerr != nil {
			// Body interrupted mid-stream: close the stream with the
			// error so the cloud's streamBody surfaces it.
			return out.WriteFrame(Frame{Kind: KindCloseStream, StreamID: streamID, Payload: []byte(rerr.Error())})
		}
	}
}

func (p *LocalProxy) fail(streamID uint32, out frameSink, code int, cause error) error {
	var head bytes.Buffer
	fmt.Fprintf(&head, "HTTP/1.1 %d %s\r\n", code, http.StatusText(code))
	head.WriteString("Content-Length: 0\r\n\r\n")
	if err := out.WriteFrame(Frame{Kind: KindResponseHead, StreamID: streamID, Payload: head.Bytes()}); err != nil {
		return err
	}
	return out.WriteFrame(Frame{Kind: KindCloseStream, StreamID: streamID, Payload: []byte(cause.Error())})
}
