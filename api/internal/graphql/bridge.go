package graphql

// The REST bridge: every root field runs the REST handler that owns the
// same operation, in process, through the API router. The sub-request
// carries the caller's context (so the same principal), headers
// (Authorization, cookies, Accept-Language, X-Device-ID, request id) and
// peer address, and passes the router's whole middleware chain: the
// per-IP and per-user rate limits, the Idempotency-Key replay, the body
// limits, logging and metrics, then the handler's own admin checks,
// library visibility and audit writes. Nothing about access is decided
// here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
)

// Bridge dispatches REST sub-requests into Router.
type Bridge struct {
	// Router is the API's root router (the chi mux every REST handler is
	// mounted on).
	Router http.Handler
}

// reqKey carries the outer GraphQL request on the resolver context.
type reqKey struct{}

type outerRequest struct {
	r *http.Request
	// idemKey is the caller's Idempotency-Key; each mutation field runs
	// its REST call under a key derived from it (see mutationKey).
	idemKey string
	seq     atomic.Int32
}

// withOuter attaches the GraphQL request to ctx for the bridge.
func withOuter(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, reqKey{}, &outerRequest{r: r, idemKey: r.Header.Get("Idempotency-Key")})
}

func outerFrom(ctx context.Context) *outerRequest {
	o, _ := ctx.Value(reqKey{}).(*outerRequest)
	return o
}

// skipHeaders are not copied from the GraphQL request onto a REST
// sub-request: they describe the GraphQL body or connection, not the
// caller.
var skipHeaders = map[string]bool{
	"Content-Length":           true,
	"Content-Type":             true,
	"Content-Encoding":         true,
	"Accept":                   true,
	"Accept-Encoding":          true,
	"Idempotency-Key":          true,
	"Connection":               true,
	"Upgrade":                  true,
	"Sec-Websocket-Key":        true,
	"Sec-Websocket-Version":    true,
	"Sec-Websocket-Protocol":   true,
	"Sec-Websocket-Extensions": true,
	"Last-Event-Id":            true,
}

// call is one REST sub-request.
type call struct {
	method   string
	path     string
	query    url.Values
	body     any // JSON-encoded when non-nil
	mutation bool
	field    string // the GraphQL field, for the derived Idempotency-Key
}

// result is a REST response.
type result struct {
	status int
	header http.Header
	body   []byte
}

// do runs c and returns the response, or a *restError for a non-2xx one.
func (b *Bridge) do(ctx context.Context, c call) (*result, error) {
	if b == nil || b.Router == nil {
		return nil, &restError{Status: http.StatusServiceUnavailable, Title: "service unavailable", Detail: "the GraphQL bridge has no API router"}
	}
	target := c.path
	if len(c.query) > 0 {
		target += "?" + c.query.Encode()
	}
	var body io.Reader = http.NoBody
	if c.body != nil {
		raw, err := json.Marshal(c.body)
		if err != nil {
			return nil, fmt.Errorf("encode %s %s: %w", c.method, c.path, err)
		}
		body = bytes.NewReader(raw)
	}
	// A fresh chi routing context: the outer request was routed by the
	// same mux, and chi would otherwise treat the sub-request as a
	// sub-router continuation of /graphql.
	subCtx := context.WithValue(ctx, chi.RouteCtxKey, nil)
	req, err := http.NewRequestWithContext(subCtx, c.method, target, body)
	if err != nil {
		return nil, err
	}
	if o := outerFrom(ctx); o != nil {
		for k, vs := range o.r.Header {
			if skipHeaders[http.CanonicalHeaderKey(k)] {
				continue
			}
			req.Header[k] = append([]string(nil), vs...)
		}
		req.RemoteAddr = o.r.RemoteAddr
		req.Host = o.r.Host
		if c.mutation && o.idemKey != "" {
			req.Header.Set("Idempotency-Key", mutationKey(o, c.field))
		}
	}
	req.Header.Set("Accept", "application/json")
	if c.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := &recorder{header: http.Header{}}
	b.Router.ServeHTTP(rec, req)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	res := &result{status: rec.status, header: rec.header, body: rec.buf.Bytes()}
	if res.status < 200 || res.status > 299 {
		return res, problemOf(res)
	}
	return res, nil
}

// json runs c and decodes a 2xx body into out (when out is non-nil).
func (b *Bridge) json(ctx context.Context, c call, out any) (*result, error) {
	res, err := b.do(ctx, c)
	if err != nil {
		return res, err
	}
	if out != nil && len(bytes.TrimSpace(res.body)) > 0 {
		if err := json.Unmarshal(res.body, out); err != nil {
			return res, fmt.Errorf("decode %s %s: %w", c.method, c.path, err)
		}
	}
	return res, nil
}

// mutationKey derives the REST Idempotency-Key of one mutation field
// from the caller's key: the key, the field's position in the operation
// and its name. A retried GraphQL request with the same key replays at
// the GraphQL layer (the router's idempotency middleware caches the
// whole /graphql response); the derived keys make each REST write
// idempotent on its own as well, without colliding with the outer record.
func mutationKey(o *outerRequest, field string) string {
	n := o.seq.Add(1)
	return o.idemKey + ":graphql:" + strconv.Itoa(int(n)) + ":" + field
}

// recorder is the sub-request's ResponseWriter.
type recorder struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.buf.Write(p)
}

// Flush satisfies http.Flusher for handlers that flush.
func (r *recorder) Flush() {}

// restError is a non-2xx REST answer, surfaced as a GraphQL error with
// the problem's fields in extensions.
type restError struct {
	Status     int
	Type       string
	Title      string
	Detail     string
	Errors     []map[string]any
	RetryAfter string
}

func (e *restError) Error() string {
	msg := e.Title
	if e.Detail != "" {
		if msg != "" {
			msg += ": "
		}
		msg += e.Detail
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return msg
}

// Extensions is read by graph-gophers into the error's extensions.
func (e *restError) Extensions() map[string]any {
	ext := map[string]any{"code": codeFor(e.Status), "status": e.Status}
	if e.Type != "" {
		ext["type"] = e.Type
	}
	if len(e.Errors) > 0 {
		ext["errors"] = e.Errors
	}
	if e.RetryAfter != "" {
		ext["retryAfter"] = e.RetryAfter
	}
	return ext
}

func problemOf(res *result) *restError {
	var p struct {
		Type   string           `json:"type"`
		Title  string           `json:"title"`
		Detail string           `json:"detail"`
		Errors []map[string]any `json:"errors"`
	}
	_ = json.Unmarshal(res.body, &p)
	if p.Title == "" && p.Detail == "" {
		p.Detail = strings.TrimSpace(string(res.body))
		if len(p.Detail) > 300 {
			p.Detail = p.Detail[:300]
		}
	}
	return &restError{
		Status: res.status, Type: p.Type, Title: p.Title, Detail: p.Detail, Errors: p.Errors,
		RetryAfter: res.header.Get("Retry-After"),
	}
}

// codeFor maps a REST status onto the GraphQL error code.
func codeFor(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "unauthenticated"
	case status == http.StatusForbidden:
		return "forbidden"
	case status == http.StatusNotFound:
		return "not-found"
	case status == http.StatusConflict:
		return "conflict"
	case status == http.StatusPreconditionFailed:
		return "confirmation-required"
	case status == http.StatusRequestEntityTooLarge:
		return "payload-too-large"
	case status == http.StatusUnprocessableEntity:
		return "validation-failed"
	case status == http.StatusTooManyRequests:
		return "rate-limited"
	case status == http.StatusServiceUnavailable:
		return "unavailable"
	case status >= 400 && status < 500:
		return "bad-request"
	default:
		return "internal"
	}
}

// isNotFound reports a REST 404, which a nullable lookup field answers
// with null.
func isNotFound(err error) bool {
	re, ok := err.(*restError)
	return ok && re.Status == http.StatusNotFound
}
