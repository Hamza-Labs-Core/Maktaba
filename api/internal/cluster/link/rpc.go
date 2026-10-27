package link

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/linkctx"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// ReqHead is a proxied request's first frame (kind req): chunks of its body follow when
// Body is set, then an end frame.
type ReqHead struct {
	Method string      `json:"method"`
	URI    string      `json:"uri"`
	Host   string      `json:"host,omitempty"`
	Header http.Header `json:"header,omitempty"`
	Body   bool        `json:"body,omitempty"`
}

// RespHead is its answer's first frame (kind resp): chunks of the body follow, then end.
// A small answer is one frame (kind whole) with its Body.
type RespHead struct {
	Status int         `json:"status"`
	Header http.Header `json:"header,omitempty"`
	Body   []byte      `json:"body,omitempty"`
}

// EndBody is an answer's end frame: Moved when the rest of the body goes on a data
// stream (over 1 MiB), Error when the home could not finish it.
type EndBody struct {
	Moved *StreamTicket `json:"moved,omitempty"`
	Error string        `json:"error,omitempty"`
}

// ErrBodyTooLarge: a request body over 1 MiB on the control channel.
var ErrBodyTooLarge = errors.New("link: request body over 1 MiB on the control channel")

// bodyBuf is a bounded in-memory pipe: the control channel's reader never waits on a
// slow handler (a request body is at most MaxInline on the channel).
type bodyBuf struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	n    int
	max  int
	done bool
	err  error
}

func newBodyBuf(limit int) *bodyBuf {
	b := &bodyBuf{max: limit}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *bodyBuf) write(p []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return io.ErrClosedPipe
	}
	b.n += len(p)
	if b.max > 0 && b.n > b.max {
		b.done, b.err = true, ErrBodyTooLarge
		b.cond.Broadcast()
		return ErrBodyTooLarge
	}
	b.buf = append(b.buf, p...)
	b.cond.Broadcast()
	return nil
}

func (b *bodyBuf) closeWith(err error) {
	b.mu.Lock()
	if !b.done {
		b.done, b.err = true, err
	}
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *bodyBuf) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.buf) == 0 && !b.done {
		b.cond.Wait()
	}
	if len(b.buf) == 0 {
		if b.err != nil {
			return 0, b.err
		}
		return 0, io.EOF
	}
	n := copy(p, b.buf)
	b.buf = b.buf[n:]
	return n, nil
}

func (b *bodyBuf) Close() error { b.closeWith(io.ErrClosedPipe); return nil }

// errAnswerCut ends an answer moved to a data stream whose request was cut.
var errAnswerCut = errors.New("link: the answer was cut")

// inflight is a request the home serves for a machine.
type inflight struct {
	id     uint32
	cancel context.CancelFunc
	in     *bodyBuf
}

func (in *inflight) body(f Frame) {
	if f.Kind == KindEnd {
		in.in.closeWith(nil)
		return
	}
	if err := in.in.write(f.Payload); err != nil {
		in.cancel()
	}
}

func (c *ctrl) failInflight() {
	c.mu.Lock()
	reqs := c.reqs
	c.reqs = map[uint32]*inflight{}
	c.mu.Unlock()
	for _, in := range reqs {
		in.cancel()
		in.in.closeWith(io.ErrUnexpectedEOF)
	}
}

func (c *ctrl) onReq(f Frame) {
	var h ReqHead
	if err := json.Unmarshal(f.Payload, &h); err != nil || h.Method == "" || h.URI == "" || h.URI[0] != '/' {
		c.endWith(f.ID, http.StatusBadRequest, "malformed request")
		return
	}
	ctx, cancel := context.WithCancel(linkctx.WithNode(c.ctx, c.node))
	in := &inflight{id: f.ID, cancel: cancel, in: newBodyBuf(MaxInline)}
	if !h.Body {
		in.in.closeWith(nil)
	}
	c.mu.Lock()
	if _, dup := c.reqs[f.ID]; dup {
		c.mu.Unlock()
		cancel()
		return
	}
	c.reqs[f.ID] = in
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, h.Method, h.URI, in.in)
	if err != nil {
		c.forget(f.ID)
		cancel()
		c.endWith(f.ID, http.StatusBadRequest, "malformed request")
		return
	}
	if !h.Body {
		req.Body = http.NoBody
	}
	req.Header = h.Header
	if req.Header == nil {
		req.Header = http.Header{}
	}
	req.Host = h.Host
	req.RemoteAddr = c.remote
	req.RequestURI = h.URI
	go func() {
		defer cancel()
		defer c.forget(f.ID)
		defer safego.Recover(c.srv.log(), "link_rpc")
		w := &rpcWriter{c: c, id: f.ID, ctx: ctx, header: http.Header{}}
		h := c.srv.Handler
		if h == nil {
			h = http.NotFoundHandler()
		}
		h.ServeHTTP(w, req)
		if ctx.Err() != nil {
			// the request was cut (the session is ending, or the machine cancelled it): a
			// handler that returned on that is no answer, and nothing it wrote may arrive as
			// a complete one (an empty 200, a short body); the machine sees link_lost and
			// tries a GET again on its next session. An answer moved to a data stream ends
			// there as cut (its stream closes as an error, never a body's end), so the
			// stream does not outlive the request.
			if w.moved != nil {
				_ = w.moved.CloseWithError(errAnswerCut)
			}
			return
		}
		w.finish()
	}()
}

func (c *ctrl) forget(id uint32) {
	c.mu.Lock()
	delete(c.reqs, id)
	c.mu.Unlock()
}

// endWith answers a request the home could not take with status and an end frame.
func (c *ctrl) endWith(id uint32, status int, detail string) {
	head, _ := json.Marshal(RespHead{Status: status, Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}}})
	_ = c.out.send(c.ctx, Frame{ID: id, Kind: KindResp, Class: ClassRPC, Payload: head})
	_ = c.out.send(c.ctx, Frame{ID: id, Kind: KindChunk, Class: ClassRPC, Payload: []byte(detail)})
	_ = c.out.send(c.ctx, Frame{ID: id, Kind: KindEnd, Class: ClassRPC})
}

// rpcWriter is the ResponseWriter of a request served on the control channel: resp,
// chunks of at most 16 KiB, end. Past MaxInline the rest of the body moves to a data
// stream (svc_body) the machine opens with the ticket in the end frame.
type rpcWriter struct {
	c      *ctrl
	id     uint32
	ctx    context.Context
	header http.Header
	status int
	// headSent: the resp frame went (on the first flush or when the body outgrows a
	// frame); an answer that ends before goes whole in one frame
	headSent bool
	buf      []byte
	sent     int
	err      error
	// moved: the rest goes to the svc_body stream through this pipe
	moved *io.PipeWriter
	// done is set when the handler ended its answer; a request cancelled before that cuts
	// the stream (cut), one ended after it lets the stream finish its last bytes
	done atomic.Bool
}

func (w *rpcWriter) Header() http.Header { return w.header }

func (w *rpcWriter) WriteHeader(code int) {
	if w.status != 0 || w.err != nil {
		return
	}
	if code < 100 || code > 999 {
		code = http.StatusInternalServerError
	}
	if code >= 100 && code < 200 {
		return // an informational answer is not forwarded
	}
	w.status = code
}

// sendHead sends the resp frame (once).
func (w *rpcWriter) sendHead() {
	if w.headSent || w.err != nil {
		return
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.headSent = true
	head, err := json.Marshal(RespHead{Status: w.status, Header: w.header.Clone()})
	if err == nil && len(head) > MaxPayload {
		err = ErrFrameTooLarge
	}
	if err != nil {
		w.err = err
		head, _ = json.Marshal(RespHead{Status: http.StatusBadGateway})
	}
	if serr := w.c.out.send(w.ctx, Frame{ID: w.id, Kind: KindResp, Class: ClassRPC, Payload: head}); serr != nil && w.err == nil {
		w.err = serr
	}
}

func (w *rpcWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.err != nil {
		return 0, w.err
	}
	if w.moved != nil {
		return w.moved.Write(p)
	}
	if w.sent+len(w.buf)+len(p) > MaxInline {
		if err := w.move(); err != nil {
			return 0, err
		}
		return w.moved.Write(p)
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) >= MaxPayload {
		w.sendHead()
		w.flushFull()
	}
	return len(p), w.err
}

// flushFull sends every whole 16 KiB of the buffer.
func (w *rpcWriter) flushFull() {
	for len(w.buf) >= MaxPayload && w.err == nil {
		w.sendChunk(w.buf[:MaxPayload])
		w.buf = w.buf[MaxPayload:]
	}
}

func (w *rpcWriter) sendChunk(p []byte) {
	if err := w.c.out.send(w.ctx, Frame{ID: w.id, Kind: KindChunk, Class: ClassRPC, Payload: append([]byte(nil), p...)}); err != nil {
		w.err = err
	}
	w.sent += len(p)
}

// Flush sends what is buffered (an event stream's lines go at once).
func (w *rpcWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.moved != nil || w.err != nil {
		return
	}
	w.sendHead()
	for _, p := range split(w.buf) {
		w.sendChunk(p)
	}
	w.buf = w.buf[:0]
}

// move sends the end frame with a svc_body ticket; the buffered bytes and the rest of the
// body go to that stream once the machine opens it (it must within the ticket's life).
func (w *rpcWriter) move() error {
	w.sendHead()
	pr, pw := io.Pipe()
	cut := make(chan struct{})
	var cutOnce sync.Once
	cutNow := func() { cutOnce.Do(func() { close(cut) }) }
	tok, taken, reason := w.c.srv.issueBody(w.c, cut, pr)
	if tok == "" {
		w.err = errors.New("link: no stream for a large answer: " + reason)
		_ = pr.Close()
		cutNow()
		return w.err
	}
	end, _ := json.Marshal(EndBody{Moved: &StreamTicket{T: "stream.ticket", Purpose: PurposeSvcBody, Ticket: tok,
		URL: PathStream, ExpiresAt: w.c.srv.now().Add(ticketLife).UTC()}})
	for _, p := range split(w.buf) {
		w.sendChunk(p)
	}
	w.buf = nil
	if err := w.c.out.send(w.ctx, Frame{ID: w.id, Kind: KindEnd, Class: ClassRPC, Payload: end}); err != nil {
		w.err = err
		_ = pr.Close()
		return err
	}
	w.moved = pw
	// a machine that never opens the stream must not hold the handler for ever; a request
	// cancelled before its answer ended ends the stream's body and cuts the stream (a write
	// to a machine that stopped reading is not left blocked)
	context.AfterFunc(w.ctx, func() {
		_ = pw.CloseWithError(context.Canceled)
		if !w.done.Load() {
			cutNow()
		}
	})
	go func() {
		select {
		case <-taken:
		case <-time.After(ticketLife + 5*time.Second):
			_ = pw.CloseWithError(errors.New("link: the machine did not open the stream of a large answer"))
		}
	}()
	return nil
}

func (w *rpcWriter) finish() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.moved != nil {
		w.done.Store(true)
		_ = w.moved.Close()
		return
	}
	if w.err != nil {
		w.sendHead()
		end, _ := json.Marshal(EndBody{Error: w.err.Error()})
		_ = w.c.out.send(context.Background(), Frame{ID: w.id, Kind: KindEnd, Class: ClassRPC, Payload: end})
		return
	}
	if !w.headSent {
		// the whole answer in one frame when it fits
		if whole, err := json.Marshal(RespHead{Status: w.status, Header: w.header.Clone(), Body: w.buf}); err == nil &&
			len(whole) <= MaxPayload {
			w.headSent = true
			_ = w.c.out.send(w.ctx, Frame{ID: w.id, Kind: KindWhole, Class: ClassRPC, Payload: whole})
			return
		}
	}
	w.Flush()
	_ = w.c.out.send(w.ctx, Frame{ID: w.id, Kind: KindEnd, Class: ClassRPC})
}
